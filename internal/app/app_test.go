package app

import (
	"context"
	"testing"
)

func TestRunRequiresLogOutput(t *testing.T) {
	t.Parallel()

	err := Run(context.Background(), nil, Options{
		ServiceName:        "test-service",
		DefaultHTTPAddress: ":0",
	})
	if err == nil {
		t.Fatal("Run() error = nil, want an error")
	}
}
