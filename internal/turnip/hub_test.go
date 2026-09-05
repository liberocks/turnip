package turnip

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOriginPolicy(t *testing.T) {
	hub := NewHub(nil, nil, NewMetrics(), "test", []string{"https://example.com"})
	tests := []struct {
		origin string
		want   bool
	}{
		{origin: "", want: true},
		{origin: "https://example.com", want: true},
		{origin: "HTTPS://EXAMPLE.COM", want: true},
		{origin: "https://evil.example", want: false},
		{origin: "not-a-url", want: false},
	}
	for _, test := range tests {
		request := httptest.NewRequest("GET", "/v1/ws", nil)
		request.Header.Set("Origin", test.origin)
		if got := hub.checkOrigin(request); got != test.want {
			t.Fatalf("origin %q: got %v, want %v", test.origin, got, test.want)
		}
	}
	wildcard := NewHub(nil, nil, NewMetrics(), "test", []string{"*"})
	request := httptest.NewRequest("GET", "/v1/ws", strings.NewReader(""))
	request.Header.Set("Origin", "https://any.example")
	if !wildcard.checkOrigin(request) {
		t.Fatal("explicit wildcard should allow browser origins")
	}
}
