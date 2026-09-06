package core

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
)

func TestTelemetryBoundaries(t *testing.T) {
	var logs bytes.Buffer
	s := &Service{telemetry: newTelemetry(), log: zerolog.New(&logs)}
	handler := s.observeHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := middleware.GetReqID(r.Context())
		if len(id) != 64 || id == r.Header.Get("X-Request-ID") {
			t.Error("request ID was not generated locally")
		}
		if _, ok := w.(http.Flusher); !ok {
			t.Error("SSE flushing lost")
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	for _, path := range []string{"/api/providah.v1.ConsoleService/ListResources", "/api/oidc/callback?code=secret-test-value", "/private/secret-test-value"} {
		req := httptest.NewRequest("POST", path, strings.NewReader("secret-test-value"))
		req.Header.Set("X-Request-ID", "secret-test-value")
		req.Header.Set("Authorization", "secret-test-value")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 403 || len(response.Header().Get("X-Request-ID")) != 64 {
			t.Fatal("response/status correlation failed")
		}
	}
	metrics := httptest.NewRecorder()
	s.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest("GET", "/metrics", nil))
	for _, body := range []string{logs.String(), metrics.Body.String()} {
		if strings.Contains(body, "secret-test-value") || strings.Contains(body, "/private/") {
			t.Fatal("sensitive input reached telemetry")
		}
	}
	text := metrics.Body.String()
	for _, want := range []string{`providah_http_request_seconds_count{method="POST",route="other",status="403"} 1`, `providah_http_active_requests 0`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing metric %s", want)
		}
	}
	if telemetryRoute("/api/providah.v1.ConsoleService/ListResources/extra") != "other" {
		t.Fatal("unbounded method accepted")
	}
}
