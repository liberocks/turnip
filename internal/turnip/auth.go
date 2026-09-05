package turnip

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
)

var roomIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type Claims struct {
	RoomID string `json:"room_id"`
	Name   string `json:"name,omitempty"`
	jwt.RegisteredClaims
}

func (c Claims) Validate() error {
	if strings.TrimSpace(c.Subject) == "" || len(c.Subject) > 256 || !utf8.ValidString(c.Subject) {
		return errors.New("subject is required")
	}
	if !roomIDPattern.MatchString(c.RoomID) {
		return errors.New("room_id is invalid")
	}
	if !utf8.ValidString(c.Name) || utf8.RuneCountInString(c.Name) > 80 {
		return errors.New("name exceeds 80 characters")
	}
	return nil
}

type Identity struct {
	Subject string
	RoomID  string
	Name    string
}

type Authenticator struct {
	secret   []byte
	issuer   string
	audience string
	now      func() time.Time
}

func NewAuthenticator(secret, issuer, audience string) *Authenticator {
	return &Authenticator{secret: []byte(secret), issuer: issuer, audience: audience, now: time.Now}
}

func (a *Authenticator) AuthenticateRequest(r *http.Request) (Identity, error) {
	tokenString, err := tokenFromRequest(r)
	if err != nil {
		return Identity{}, err
	}
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing algorithm")
		}
		return a.secret, nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(a.issuer),
		jwt.WithAudience(a.audience),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(a.now),
	)
	if err != nil || !token.Valid {
		return Identity{}, errors.New("invalid token")
	}
	return Identity{Subject: claims.Subject, RoomID: claims.RoomID, Name: claims.Name}, nil
}

func tokenFromRequest(r *http.Request) (string, error) {
	queryValues := r.URL.Query()["token"]
	if len(queryValues) > 1 {
		return "", errors.New("multiple query credentials")
	}
	queryToken := ""
	if len(queryValues) == 1 {
		queryToken = queryValues[0]
	}
	if len(r.Header.Values("Authorization")) > 1 {
		return "", errors.New("multiple authorization headers")
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	headerToken := ""
	if header != "" {
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
			return "", errors.New("invalid authorization header")
		}
		headerToken = strings.TrimSpace(parts[1])
	}
	if queryToken != "" && headerToken != "" && queryToken != headerToken {
		return "", errors.New("conflicting credentials")
	}
	if headerToken != "" {
		if len(headerToken) > 8192 {
			return "", errors.New("bearer token is too large")
		}
		return headerToken, nil
	}
	if queryToken != "" {
		if len(queryToken) > 8192 {
			return "", errors.New("bearer token is too large")
		}
		return queryToken, nil
	}
	return "", errors.New("missing bearer token")
}

func GenerateToken(secret, issuer, audience, subject, roomID, name string, lifetime time.Duration, now time.Time) (string, error) {
	claims := Claims{
		RoomID: roomID,
		Name:   name,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(now.Add(lifetime)),
		},
	}
	if err := claims.Validate(); err != nil {
		return "", err
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}
