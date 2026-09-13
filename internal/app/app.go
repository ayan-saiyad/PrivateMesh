// Package app defines the shared process lifecycle for PrivateMesh services.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/ayansaiyad/privatemesh/internal/config"
	"github.com/ayansaiyad/privatemesh/internal/httpserver"
)

// Options contains the values that distinguish one service executable from another.
type Options struct {
	ServiceName        string
	DefaultHTTPAddress string
}

// Healthcheck verifies that the local process health endpoint is accepting requests.
func Healthcheck(ctx context.Context, options Options) error {
	cfg, err := config.Load(config.Defaults{
		ServiceName: options.ServiceName,
		HTTPAddress: options.DefaultHTTPAddress,
	})
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	_, port, err := net.SplitHostPort(cfg.HTTPAddress)
	if err != nil {
		return fmt.Errorf("parse HTTP address: %w", err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://"+net.JoinHostPort("127.0.0.1", port)+"/healthz",
		nil,
	)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("request health endpoint: %w", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned status %d", response.StatusCode)
	}
	return nil
}

// Run loads configuration and serves requests until the process receives a shutdown signal.
func Run(parent context.Context, output io.Writer, options Options) error {
	if output == nil {
		return errors.New("log output is required")
	}

	cfg, err := config.Load(config.Defaults{
		ServiceName: options.ServiceName,
		HTTPAddress: options.DefaultHTTPAddress,
	})
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server := httpserver.New(cfg, logger)
	return server.Run(ctx)
}
