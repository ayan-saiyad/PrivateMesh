// Package config loads and validates PrivateMesh process configuration.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"
)

const (
	defaultShutdownTimeout = 10 * time.Second
	defaultDataDir         = "/var/lib/privatemesh"
)

// Defaults contains executable-specific configuration defaults.
type Defaults struct {
	ServiceName string
	HTTPAddress string
	GRPCAddress string
}

// Config is the validated process configuration shared by backend services.
type Config struct {
	ServiceName     string
	HTTPAddress     string
	GRPCAddress     string
	ShutdownTimeout time.Duration
	DataDir         string
	LogLevel        slog.Level
}

// Load reads process configuration from the environment and applies the provided defaults.
func Load(defaults Defaults) (Config, error) {
	if strings.TrimSpace(defaults.ServiceName) == "" {
		return Config{}, errors.New("service name is required")
	}
	if err := validateAddress(defaults.HTTPAddress); err != nil {
		return Config{}, fmt.Errorf("default HTTP address: %w", err)
	}

	prefix := strings.ToUpper(strings.ReplaceAll(defaults.ServiceName, "-", "_"))
	address := valueOrDefault("PRIVATEMESH_"+prefix+"_HTTP_ADDRESS", defaults.HTTPAddress)
	if err := validateAddress(address); err != nil {
		return Config{}, fmt.Errorf("HTTP address: %w", err)
	}
	grpcAddress := ""
	if strings.TrimSpace(defaults.GRPCAddress) != "" {
		grpcAddress = valueOrDefault("PRIVATEMESH_"+prefix+"_GRPC_ADDRESS", defaults.GRPCAddress)
		if err := validateAddress(grpcAddress); err != nil {
			return Config{}, fmt.Errorf("gRPC address: %w", err)
		}
	}

	shutdownTimeout, err := time.ParseDuration(valueOrDefault(
		"PRIVATEMESH_SHUTDOWN_TIMEOUT",
		defaultShutdownTimeout.String(),
	))
	if err != nil || shutdownTimeout <= 0 {
		return Config{}, errors.New("shutdown timeout must be a positive duration")
	}

	logLevel, err := parseLogLevel(valueOrDefault("PRIVATEMESH_LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}

	return Config{
		ServiceName:     defaults.ServiceName,
		HTTPAddress:     address,
		GRPCAddress:     grpcAddress,
		ShutdownTimeout: shutdownTimeout,
		DataDir:         valueOrDefault("PRIVATEMESH_NODE_DATA_DIR", defaultDataDir),
		LogLevel:        logLevel,
	}, nil
}

func validateAddress(address string) error {
	if strings.TrimSpace(address) == "" {
		return errors.New("address is required")
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("must be in host:port form: %w", err)
	}
	return nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(raw))); err != nil {
		return 0, fmt.Errorf("invalid log level %q: %w", raw, err)
	}
	return level, nil
}

func valueOrDefault(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
