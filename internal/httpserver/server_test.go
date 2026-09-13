package httpserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ayansaiyad/privatemesh/internal/config"
)

func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	server := New(config.Config{
		ServiceName:     "test-node",
		HTTPAddress:     ":0",
		ShutdownTimeout: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	if body := response.Body.String(); !strings.Contains(body, `"service":"test-node"`) {
		t.Fatalf("body = %q, want service name", body)
	}
}
