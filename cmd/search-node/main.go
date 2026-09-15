// Package main starts a PrivateMesh search-node service.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ayansaiyad/privatemesh/internal/app"
	searchnodeservice "github.com/ayansaiyad/privatemesh/internal/searchnode"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	options := app.Options{
		ServiceName:        "search-node",
		DefaultHTTPAddress: ":8090",
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
	err := searchnodeservice.Run(ctx, os.Stdout, searchnodeservice.RuntimeOptions{
		HTTPAddress: ":8090", GRPCAddress: ":8091",
	})
	stop()
	if err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}
