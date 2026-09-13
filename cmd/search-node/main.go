// Package main starts a PrivateMesh search-node service.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/ayansaiyad/privatemesh/internal/app"
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

	if err := app.Run(context.Background(), os.Stdout, options); err != nil {
		logger.Error("service stopped", "error", err)
		os.Exit(1)
	}
}
