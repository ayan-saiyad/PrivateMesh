// Package httpserver provides the shared HTTP endpoints for PrivateMesh services.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ayansaiyad/privatemesh/internal/config"
	"github.com/ayansaiyad/privatemesh/internal/version"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
)

// Server exposes the process-level HTTP endpoints shared by backend services.
type Server struct {
	config config.Config
	logger *slog.Logger
	server *http.Server
}

// New constructs a server with process endpoints and optional service routes.
func New(cfg config.Config, logger *slog.Logger, registrars ...func(*http.ServeMux)) *Server {
	return newServer(cfg, logger, nil, registrars...)
}

// NewWithMiddleware constructs a server whose service and process routes share one middleware.
func NewWithMiddleware(
	cfg config.Config,
	logger *slog.Logger,
	middleware func(http.Handler) http.Handler,
	registrars ...func(*http.ServeMux),
) *Server {
	return newServer(cfg, logger, middleware, registrars...)
}

func newServer(
	cfg config.Config,
	logger *slog.Logger,
	middleware func(http.Handler) http.Handler,
	registrars ...func(*http.ServeMux),
) *Server {
	mux := http.NewServeMux()
	s := &Server{
		config: cfg,
		logger: logger.With("service", cfg.ServiceName),
	}

	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /version", s.version)
	for _, register := range registrars {
		if register != nil {
			register(mux)
		}
	}

	handler := http.Handler(mux)
	if middleware != nil {
		handler = middleware(handler)
	}
	s.server = &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           requestLogger(s.logger, handler),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	return s
}

// Run serves requests until the context is canceled, then performs a graceful shutdown.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("HTTP server listening", "address", s.config.HTTPAddress)
		errCh <- s.server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
	defer cancel()

	s.logger.Info("shutting down HTTP server")
	if err := s.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	return nil
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"service": s.config.ServiceName,
		"status":  "ok",
	})
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"service": s.config.ServiceName,
		"status":  "ready",
	})
}

func (s *Server) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, version.Current(s.config.ServiceName))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("encode HTTP response", "error", err)
	}
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(started),
		)
	})
}
