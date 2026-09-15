package coordinator

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	privatemeshv1 "github.com/ayansaiyad/privatemesh/gen/go/privatemesh/v1"
	"github.com/ayansaiyad/privatemesh/internal/config"
	"github.com/ayansaiyad/privatemesh/internal/controlplane"
	"github.com/ayansaiyad/privatemesh/internal/distributed"
	"github.com/ayansaiyad/privatemesh/internal/httpserver"
	"github.com/ayansaiyad/privatemesh/internal/identity"
	"github.com/ayansaiyad/privatemesh/internal/planner"
	"github.com/ayansaiyad/privatemesh/internal/registry"
	"github.com/ayansaiyad/privatemesh/internal/rpcclient"
	"github.com/ayansaiyad/privatemesh/internal/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	coordinatorIssuer  = "privatemesh-coordinator"
	searchNodeAudience = "privatemesh-search-nodes"
	principalKeyID     = "coordinator-1"
)

// RuntimeOptions provides coordinator process defaults.
type RuntimeOptions struct {
	HTTPAddress string
	GRPCAddress string
}

// Run starts the coordinator HTTP and gRPC servers.
func Run(parent context.Context, output io.Writer, options RuntimeOptions) error {
	if output == nil {
		return errors.New("log output is required")
	}
	cfg, err := config.Load(config.Defaults{
		ServiceName: "coordinator", HTTPAddress: options.HTTPAddress, GRPCAddress: options.GRPCAddress,
	})
	if err != nil {
		return fmt.Errorf("load coordinator configuration: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: cfg.LogLevel}))
	instrumentation, err := telemetry.New(parent, cfg.ServiceName)
	if err != nil {
		return err
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = instrumentation.Shutdown(shutdownContext)
		cancel()
	}()
	demoMode, err := environmentBool("PRIVATEMESH_DEMO_MODE", false)
	if err != nil {
		return err
	}
	privateKey, err := coordinatorSigningKey(demoMode)
	if err != nil {
		return err
	}
	signer, err := identity.NewSigner(
		coordinatorIssuer, searchNodeAudience, principalKeyID, privateKey, 2*time.Minute,
	)
	if err != nil {
		return err
	}
	authenticator, err := browserAuthenticator(parent, demoMode)
	if err != nil {
		return err
	}
	nodeRegistry, err := registry.New(15 * time.Second)
	if err != nil {
		return err
	}
	queryPlanner, err := defaultPlanner()
	if err != nil {
		return err
	}
	nodeClient, err := rpcclient.New(
		signer,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(instrumentation.UnaryClientInterceptor),
		grpc.WithChainStreamInterceptor(instrumentation.StreamClientInterceptor),
	)
	if err != nil {
		return err
	}
	defer func() { _ = nodeClient.Close() }()
	executor, err := distributed.NewExecutor(nodeRegistry, nodeClient)
	if err != nil {
		return err
	}
	api, err := NewAPI(nodeRegistry, executor, queryPlanner, nodeClient, authenticator, instrumentation)
	if err != nil {
		return err
	}
	controlService, err := controlplane.New(nodeRegistry)
	if err != nil {
		return err
	}

	grpcListener, err := (&net.ListenConfig{}).Listen(parent, "tcp", cfg.GRPCAddress)
	if err != nil {
		return fmt.Errorf("listen for coordinator gRPC: %w", err)
	}
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(instrumentation.UnaryServerInterceptor),
		grpc.ChainStreamInterceptor(instrumentation.StreamServerInterceptor),
	)
	privatemeshv1.RegisterControlPlaneServiceServer(grpcServer, controlService)
	httpServer := httpserver.NewWithMiddleware(
		cfg, logger, instrumentation.HTTPMiddleware, api.RegisterRoutes, instrumentation.RegisterRoutes,
	)
	logger.Info("coordinator ready", "http_address", cfg.HTTPAddress, "grpc_address", cfg.GRPCAddress)
	return serveCoordinator(parent, httpServer, grpcServer, grpcListener)
}

func serveCoordinator(ctx context.Context, httpServer *httpserver.Server, grpcServer *grpc.Server, listener net.Listener) error {
	errorsChannel := make(chan error, 2)
	go func() {
		if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			errorsChannel <- fmt.Errorf("serve coordinator gRPC: %w", err)
			return
		}
		errorsChannel <- nil
	}()
	go func() { errorsChannel <- httpServer.Run(ctx) }()

	select {
	case err := <-errorsChannel:
		grpcServer.Stop()
		return err
	case <-ctx.Done():
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			grpcServer.Stop()
		}
		return <-errorsChannel
	}
}

func browserAuthenticator(ctx context.Context, demoMode bool) (Authenticator, error) {
	if demoMode {
		return StaticAuthenticator{Principal: identity.Principal{
			Subject: "demo-user", Groups: []string{"engineering"},
			Scopes: []string{"search.execute", "documents.read", "documents.write"},
		}}, nil
	}
	issuer := strings.TrimSpace(os.Getenv("PRIVATEMESH_OIDC_ISSUER"))
	audience := strings.TrimSpace(os.Getenv("PRIVATEMESH_OIDC_CLIENT_ID"))
	if issuer == "" || audience == "" {
		return nil, errors.New("OIDC issuer and client ID are required when demo mode is disabled")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	metadata, err := identity.DiscoverProvider(ctx, client, issuer)
	if err != nil {
		return nil, err
	}
	keys, err := identity.NewRemoteKeySet(client, metadata.JWKSURL, 15*time.Minute)
	if err != nil {
		return nil, err
	}
	verifier, err := identity.NewVerifier(metadata.Issuer, audience, keys, time.Minute)
	if err != nil {
		return nil, err
	}
	return NewBearerAuthenticator(verifier)
}

func coordinatorSigningKey(demoMode bool) (ed25519.PrivateKey, error) {
	raw := strings.TrimSpace(os.Getenv("PRIVATEMESH_PRINCIPAL_SIGNING_KEY"))
	if raw == "" {
		if !demoMode {
			return nil, errors.New("principal signing key is required when demo mode is disabled")
		}
		seed := sha256.Sum256([]byte("privatemesh local demonstration principal key"))
		return ed25519.NewKeyFromSeed(seed[:]), nil
	}
	decoded, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("principal signing key must be unpadded base64")
	}
	switch len(decoded) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(decoded), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(decoded), nil
	default:
		return nil, errors.New("principal signing key has an invalid length")
	}
}

func defaultPlanner() (*planner.Planner, error) {
	tracker, err := planner.NewTracker(map[planner.Strategy]planner.Baseline{
		planner.Lexical: {Quality: 0.78, P95: 15 * time.Millisecond},
		planner.Vector:  {Quality: 0.82, P95: 35 * time.Millisecond},
		planner.Hybrid:  {Quality: 0.91, P95: 60 * time.Millisecond},
	})
	if err != nil {
		return nil, err
	}
	return planner.New(tracker, 0.85)
}

func environmentBool(name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return value, nil
}
