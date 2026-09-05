package turnip

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicHTTPRoutes(t *testing.T) {
	app := &App{config: Config{Version: "test"}, metrics: NewMetrics(), hub: &Hub{}}
	handler := app.mainHandler()

	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"version":"test"`) {
		t.Fatalf("liveness response = %d %s", response.Code, response.Body.String())
	}

	for _, path := range []string{"/", "/ws", "/generate-token", "/info"} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s returned %d, want 404", path, response.Code)
		}
	}
}

func TestMetricsHandlerAuthenticationAndIndependentRegistries(t *testing.T) {
	first := NewMetrics()
	second := NewMetrics()
	first.ConnectionsTotal.Inc()
	second.ConnectionsTotal.Inc()

	handler := first.Handler("metrics", "secret")
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated metrics returned %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.SetBasicAuth("metrics", "secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "turnip_connections_total") {
		t.Fatalf("authenticated metrics response = %d", response.Code)
	}
}
