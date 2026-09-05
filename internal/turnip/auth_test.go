package turnip

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAuthenticatorValidatesStandardAndRoomClaims(t *testing.T) {
	secret := strings.Repeat("a", 32)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	authenticator := NewAuthenticator(secret, "issuer", "audience")
	authenticator.now = func() time.Time { return now }

	token, err := GenerateToken(secret, "issuer", "audience", "user-1", "room_1", "Alice", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/v1/ws?token="+token, nil)
	identity, err := authenticator.AuthenticateRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Subject != "user-1" || identity.RoomID != "room_1" || identity.Name != "Alice" {
		t.Fatalf("unexpected identity: %+v", identity)
	}

	tests := []struct {
		name   string
		claims Claims
		method jwt.SigningMethod
		key    any
	}{
		{name: "expired", claims: testClaims("issuer", "audience", "user", "room", now.Add(-time.Hour)), method: jwt.SigningMethodHS256, key: []byte(secret)},
		{name: "wrong issuer", claims: testClaims("other", "audience", "user", "room", now.Add(time.Hour)), method: jwt.SigningMethodHS256, key: []byte(secret)},
		{name: "wrong audience", claims: testClaims("issuer", "other", "user", "room", now.Add(time.Hour)), method: jwt.SigningMethodHS256, key: []byte(secret)},
		{name: "missing subject", claims: testClaims("issuer", "audience", "", "room", now.Add(time.Hour)), method: jwt.SigningMethodHS256, key: []byte(secret)},
		{name: "invalid room", claims: testClaims("issuer", "audience", "user", "bad room", now.Add(time.Hour)), method: jwt.SigningMethodHS256, key: []byte(secret)},
		{name: "wrong algorithm", claims: testClaims("issuer", "audience", "user", "room", now.Add(time.Hour)), method: jwt.SigningMethodHS384, key: []byte(secret)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signed, signErr := jwt.NewWithClaims(test.method, test.claims).SignedString(test.key)
			if signErr != nil {
				t.Fatal(signErr)
			}
			r := httptest.NewRequest("GET", "/v1/ws?token="+signed, nil)
			if _, authErr := authenticator.AuthenticateRequest(r); authErr == nil {
				t.Fatal("expected token to be rejected")
			}
		})
	}
}

func TestTokenSources(t *testing.T) {
	request := httptest.NewRequest("GET", "/v1/ws?token=query", nil)
	request.Header.Set("Authorization", "Bearer header")
	if _, err := tokenFromRequest(request); err == nil {
		t.Fatal("expected conflicting credentials to fail")
	}
	request = httptest.NewRequest("GET", "/v1/ws", nil)
	request.Header.Set("Authorization", "Bearer header")
	if token, err := tokenFromRequest(request); err != nil || token != "header" {
		t.Fatalf("token = %q, error = %v", token, err)
	}
}

func testClaims(issuer, audience, subject, room string, expires time.Time) Claims {
	return Claims{
		RoomID: room,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: issuer, Audience: jwt.ClaimStrings{audience}, Subject: subject,
			ExpiresAt: jwt.NewNumericDate(expires),
		},
	}
}
