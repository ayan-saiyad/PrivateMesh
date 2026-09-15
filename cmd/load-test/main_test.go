package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunExercisesSearchEndpoint(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(response, "POST required", http.StatusMethodNotAllowed)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"results":[]}`))
	}))
	t.Cleanup(server.Close)
	var output bytes.Buffer
	err := run([]string{
		"-url", server.URL, "-requests", "20", "-concurrency", "4", "-p95-budget", "1s",
	}, &output, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"successful": 20`)) {
		t.Fatalf("report = %s", output.String())
	}
}

func TestLatencyPercentile(t *testing.T) {
	t.Parallel()
	values := []time.Duration{4 * time.Millisecond, time.Millisecond, 3 * time.Millisecond, 2 * time.Millisecond}
	if got := latencyPercentile(values, 0.50); got != 3*time.Millisecond {
		t.Fatalf("latencyPercentile() = %s", got)
	}
}
