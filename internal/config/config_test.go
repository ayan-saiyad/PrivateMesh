package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadUsesDefaults(t *testing.T) {
	t.Setenv("PRIVATEMESH_COORDINATOR_HTTP_ADDRESS", "")
	t.Setenv("PRIVATEMESH_SHUTDOWN_TIMEOUT", "")
	t.Setenv("PRIVATEMESH_LOG_LEVEL", "")

	cfg, err := Load(Defaults{ServiceName: "coordinator", HTTPAddress: ":8080"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddress != ":8080" {
		t.Fatalf("HTTPAddress = %q, want %q", cfg.HTTPAddress, ":8080")
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 10*time.Second)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelInfo)
	}
}

func TestLoadRejectsInvalidAddress(t *testing.T) {
	t.Setenv("PRIVATEMESH_SEARCH_NODE_HTTP_ADDRESS", "not-an-address")

	_, err := Load(Defaults{ServiceName: "search-node", HTTPAddress: ":8090"})
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}

func TestLoadRejectsInvalidShutdownTimeout(t *testing.T) {
	t.Setenv("PRIVATEMESH_SHUTDOWN_TIMEOUT", "0s")

	_, err := Load(Defaults{ServiceName: "coordinator", HTTPAddress: ":8080"})
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
}
