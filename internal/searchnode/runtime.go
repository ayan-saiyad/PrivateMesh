package searchnode

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
	"os"
	"strconv"
	"strings"
	"time"

	privatemeshv1 "github.com/ayansaiyad/privatemesh/gen/go/privatemesh/v1"
	"github.com/ayansaiyad/privatemesh/internal/authorization"
	"github.com/ayansaiyad/privatemesh/internal/config"
	"github.com/ayansaiyad/privatemesh/internal/embedding"
	"github.com/ayansaiyad/privatemesh/internal/httpserver"
	"github.com/ayansaiyad/privatemesh/internal/identity"
	"github.com/ayansaiyad/privatemesh/internal/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	coordinatorIssuer  = "privatemesh-coordinator"
	searchNodeAudience = "privatemesh-search-nodes"
	principalKeyID     = "coordinator-1"
	defaultDimensions  = 256
)

// RuntimeOptions provides search-node process defaults.
type RuntimeOptions struct {
	HTTPAddress string
	GRPCAddress string
}

// Run starts one owning search node and maintains its coordinator lease.
func Run(parent context.Context, output io.Writer, options RuntimeOptions) error {
	if output == nil {
		return errors.New("log output is required")
	}
	cfg, err := config.Load(config.Defaults{
		ServiceName: "search-node", HTTPAddress: options.HTTPAddress, GRPCAddress: options.GRPCAddress,
	})
	if err != nil {
		return fmt.Errorf("load search node configuration: %w", err)
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
	demoMode, err := nodeEnvironmentBool("PRIVATEMESH_DEMO_MODE", false)
	if err != nil {
		return err
	}
	nodeID := environmentValue("PRIVATEMESH_NODE_ID", "search-node")
	collectionID := environmentValue("PRIVATEMESH_COLLECTION_ID", "documents")
	advertisedAddress := environmentValue("PRIVATEMESH_NODE_ADVERTISE_ADDRESS", cfg.GRPCAddress)
	coordinatorAddress := environmentValue("PRIVATEMESH_COORDINATOR_GRPC_ADDRESS", "127.0.0.1:8081")

	publicKey, err := coordinatorPublicKey(demoMode)
	if err != nil {
		return err
	}
	verifier, err := identity.NewVerifier(
		coordinatorIssuer, searchNodeAudience, identity.StaticKeySet{principalKeyID: publicKey}, time.Minute,
	)
	if err != nil {
		return err
	}
	authenticator, err := NewTokenAuthenticator(verifier)
	if err != nil {
		return err
	}
	audit, err := authorization.NewLogAuditSink(logger.With("component", "authorization"))
	if err != nil {
		return err
	}
	policies := authorization.NewMemoryStore()
	if err := policies.SetCollectionPolicy(collectionID, defaultCollectionPolicy()); err != nil {
		return err
	}
	authorizer, err := authorization.NewEnforcer(policies, audit, auditFingerprintKey(demoMode))
	if err != nil {
		return err
	}
	embedder, err := embedding.NewHash(defaultDimensions)
	if err != nil {
		return err
	}
	engine, err := Open(parent, Options{
		CollectionID: collectionID, DataDirectory: cfg.DataDir, Dimensions: defaultDimensions,
		Embedder: embedder, Authorizer: authorizer,
	})
	if err != nil {
		return err
	}
	defer func() { _ = engine.Close() }()
	if demoMode && engine.Offset() == 0 {
		if err := seedDocuments(parent, engine); err != nil {
			return err
		}
	}
	service, err := NewService(nodeID, engine, authenticator)
	if err != nil {
		return err
	}
	grpcListener, err := (&net.ListenConfig{}).Listen(parent, "tcp", cfg.GRPCAddress)
	if err != nil {
		return fmt.Errorf("listen for search node gRPC: %w", err)
	}
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(instrumentation.UnaryServerInterceptor),
		grpc.ChainStreamInterceptor(instrumentation.StreamServerInterceptor),
	)
	service.Register(grpcServer)
	httpServer := httpserver.NewWithMiddleware(
		cfg, logger, instrumentation.HTTPMiddleware, instrumentation.RegisterRoutes,
	)
	logger.Info("search node ready",
		"node_id", nodeID, "collection_id", collectionID,
		"http_address", cfg.HTTPAddress, "grpc_address", cfg.GRPCAddress,
	)
	return serveNode(
		parent, httpServer, grpcServer, grpcListener, logger,
		registrationOptions{
			CoordinatorAddress: coordinatorAddress, NodeID: nodeID, AdvertisedAddress: advertisedAddress,
			CollectionID: collectionID, Offset: engine.Offset, Telemetry: instrumentation,
		},
	)
}

func serveNode(
	ctx context.Context,
	httpServer *httpserver.Server,
	grpcServer *grpc.Server,
	listener net.Listener,
	logger *slog.Logger,
	registration registrationOptions,
) error {
	errorsChannel := make(chan error, 3)
	go func() {
		if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			errorsChannel <- fmt.Errorf("serve search node gRPC: %w", err)
			return
		}
		errorsChannel <- nil
	}()
	go func() { errorsChannel <- httpServer.Run(ctx) }()
	go func() { errorsChannel <- maintainRegistration(ctx, logger, registration) }()

	select {
	case err := <-errorsChannel:
		grpcServer.Stop()
		return err
	case <-ctx.Done():
		grpcServer.GracefulStop()
		return <-errorsChannel
	}
}

type registrationOptions struct {
	CoordinatorAddress string
	NodeID             string
	AdvertisedAddress  string
	CollectionID       string
	Offset             func() uint64
	Telemetry          *telemetry.Telemetry
}

func maintainRegistration(ctx context.Context, logger *slog.Logger, options registrationOptions) error {
	dialOptions := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if options.Telemetry != nil {
		dialOptions = append(dialOptions,
			grpc.WithChainUnaryInterceptor(options.Telemetry.UnaryClientInterceptor),
			grpc.WithChainStreamInterceptor(options.Telemetry.StreamClientInterceptor),
		)
	}
	connection, err := grpc.NewClient(options.CoordinatorAddress, dialOptions...)
	if err != nil {
		return fmt.Errorf("create coordinator client: %w", err)
	}
	defer func() { _ = connection.Close() }()
	client := privatemeshv1.NewControlPlaneServiceClient(connection)
	var leaseID string
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		if leaseID == "" {
			attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
			response, registerErr := client.RegisterNode(attempt, &privatemeshv1.RegisterNodeRequest{
				Node: &privatemeshv1.NodeDescriptor{
					NodeId: options.NodeID, Address: options.AdvertisedAddress,
					CollectionIds: []string{options.CollectionID}, Labels: map[string]string{"vector": "ready"},
				},
			})
			cancel()
			if registerErr == nil {
				leaseID = response.GetLeaseId()
				logger.Info("registered with coordinator", "node_id", options.NodeID)
			} else if ctx.Err() == nil {
				logger.Warn("coordinator registration failed", "error", registerErr)
			}
		}

		select {
		case <-ctx.Done():
			if leaseID != "" {
				shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, _ = client.DeregisterNode(shutdownContext, &privatemeshv1.DeregisterNodeRequest{
					NodeId: options.NodeID, LeaseId: leaseID,
				})
				cancel()
			}
			return nil
		case <-ticker.C:
			if leaseID == "" {
				continue
			}
			attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
			_, heartbeatErr := client.Heartbeat(attempt, &privatemeshv1.HeartbeatRequest{
				NodeId: options.NodeID, LeaseId: leaseID, AppliedLogOffset: options.Offset(),
			})
			cancel()
			if heartbeatErr != nil {
				if status.Code(heartbeatErr) == codes.NotFound || status.Code(heartbeatErr) == codes.PermissionDenied {
					leaseID = ""
				}
				if ctx.Err() == nil {
					logger.Warn("coordinator heartbeat failed", "error", heartbeatErr)
				}
			}
		}
	}
}

func defaultCollectionPolicy() authorization.Policy {
	return authorization.Policy{
		Version: "1",
		Rules: []authorization.Rule{
			{Effect: authorization.EffectAllow, Actions: []authorization.Action{authorization.ActionSearch}, Scopes: []string{"search.execute"}},
			{Effect: authorization.EffectAllow, Actions: []authorization.Action{authorization.ActionRead}, Scopes: []string{"documents.read"}},
		},
	}
}

func seedDocuments(ctx context.Context, engine *Engine) error {
	common := []Document{
		{
			ID: "distributed-search", Title: "Distributed search architecture",
			Content: "The coordinator fans queries out to owning nodes, merges ranked results, and never stores source document content.",
		},
		{
			ID: "recovery-runbook", Title: "Replica recovery runbook",
			Content: "A recovering replica replays committed log entries when history is retained and installs a snapshot after compaction.",
		},
	}
	collectionSpecific := Document{
		ID: "local-ownership", Title: "Local ownership boundary",
		Content: "Documents and indexes remain under the control of the node that owns the " + engine.CollectionID() + " collection.",
	}
	for _, document := range append(common, collectionSpecific) {
		if _, err := engine.Upsert(ctx, document); err != nil {
			return fmt.Errorf("seed document %s: %w", document.ID, err)
		}
	}
	return nil
}

func coordinatorPublicKey(demoMode bool) (ed25519.PublicKey, error) {
	raw := strings.TrimSpace(os.Getenv("PRIVATEMESH_PRINCIPAL_VERIFYING_KEY"))
	if raw == "" {
		if !demoMode {
			return nil, errors.New("principal verifying key is required when demo mode is disabled")
		}
		seed := sha256.Sum256([]byte("privatemesh local demonstration principal key"))
		privateKey := ed25519.NewKeyFromSeed(seed[:])
		return privateKey.Public().(ed25519.PublicKey), nil
	}
	decoded, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("principal verifying key must be an unpadded base64 Ed25519 public key")
	}
	return ed25519.PublicKey(decoded), nil
}

func auditFingerprintKey(demoMode bool) []byte {
	raw := strings.TrimSpace(os.Getenv("PRIVATEMESH_AUDIT_FINGERPRINT_KEY"))
	if raw != "" {
		return []byte(raw)
	}
	if demoMode {
		digest := sha256.Sum256([]byte("privatemesh local audit fingerprint key"))
		return digest[:]
	}
	return nil
}

func nodeEnvironmentBool(name string, fallback bool) (bool, error) {
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

func environmentValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
