package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPMetricsExcludeRequestPath(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	telemetry, err := New(context.Background(), "test-service")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = telemetry.Shutdown(context.Background()) })

	handler := telemetry.HTTPMiddleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/documents/secret-document-name?q=private-query", nil,
	)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	response := httptest.NewRecorder()
	telemetry.MetricsHandler().ServeHTTP(response, httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/metrics", nil,
	))
	result := response.Result()
	defer func() { _ = result.Body.Close() }()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	output := string(body)
	if !strings.Contains(output, "privatemesh_http_requests_total") {
		t.Fatalf("metrics output is missing request counter: %s", output)
	}
	for _, protected := range []string{"secret-document-name", "private-query"} {
		if strings.Contains(output, protected) {
			t.Fatalf("metrics output exposes protected value %q", protected)
		}
	}
}
