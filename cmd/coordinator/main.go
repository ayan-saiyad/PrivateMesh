// Package main starts the PrivateMesh coordinator service.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ayansaiyad/privatemesh/internal/app"
	coordinatorservice "github.com/ayansaiyad/privatemesh/internal/coordinator"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	options := app.Options{
		ServiceName:        "coordinator",
		DefaultHTTPAddress: ":8080",
	}

	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := app.Healthcheck(ctx, options)
		cancel()
		if err != nil {
			os.Exit(1)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	err := coordinatorservice.Run(ctx, os.Stdout, coordinatorservice.RuntimeOptions{
		HTTPAddress: ":8080", GRPCAddress: ":8081",
	})
	stop()
	if err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}
