package coordinator

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	privatemeshv1 "github.com/ayansaiyad/privatemesh/gen/go/privatemesh/v1"
	"github.com/ayansaiyad/privatemesh/internal/authorization"
	"github.com/ayansaiyad/privatemesh/internal/distributed"
	"github.com/ayansaiyad/privatemesh/internal/embedding"
	"github.com/ayansaiyad/privatemesh/internal/identity"
	"github.com/ayansaiyad/privatemesh/internal/registry"
	"github.com/ayansaiyad/privatemesh/internal/rpcclient"
	"github.com/ayansaiyad/privatemesh/internal/searchnode"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestDocumentLifecycleAcrossCoordinatorAndSearchNode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := identity.NewSigner(
		coordinatorIssuer, searchNodeAudience, principalKeyID, privateKey, time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := identity.NewVerifier(
		coordinatorIssuer, searchNodeAudience, identity.StaticKeySet{principalKeyID: publicKey}, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	nodeAuthenticator, err := searchnode.NewTokenAuthenticator(verifier)
	if err != nil {
		t.Fatal(err)
	}
	policyStore := authorization.NewMemoryStore()
	if err := policyStore.SetCollectionPolicy("engineering", authorization.Policy{
		Version: "1",
		Rules: []authorization.Rule{
			{Effect: authorization.EffectAllow, Actions: []authorization.Action{authorization.ActionSearch}, Scopes: []string{"search.execute"}},
			{Effect: authorization.EffectAllow, Actions: []authorization.Action{authorization.ActionRead}, Scopes: []string{"documents.read"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	authorizer, err := authorization.NewEnforcer(
		policyStore, &authorization.MemoryAuditSink{}, []byte("0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	embedder, err := embedding.NewHash(128)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := searchnode.Open(ctx, searchnode.Options{
		CollectionID: "engineering", DataDirectory: t.TempDir(), Dimensions: 128,
		Embedder: embedder, Authorizer: authorizer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	nodeService, err := searchnode.NewService("node-a", engine, nodeAuthenticator)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	nodeService.Register(grpcServer)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	nodeClient, err := rpcclient.New(
		signer,
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nodeClient.Close() })
	nodeRegistry, err := registry.New(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nodeRegistry.Register(registry.Node{
		ID: "node-a", Address: "passthrough:///buffer", CollectionIDs: []string{"engineering"}, Labels: map[string]string{"vector": "ready"},
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := distributed.NewExecutor(nodeRegistry, nodeClient)
	if err != nil {
		t.Fatal(err)
	}
	queryPlanner, err := defaultPlanner()
	if err != nil {
		t.Fatal(err)
	}
	principal := identity.Principal{
		Subject: "test-user", Groups: []string{"engineering"},
		Scopes: []string{"search.execute", "documents.read", "documents.write"},
	}
	probeOffset, err := nodeClient.UpsertDocument(ctx, nodeRegistry.Nodes(nil)[0], principal, "probe-write", "engineering", &privatemeshv1.Document{
		DocumentId: "transport-probe", Title: "Transport probe", Content: "signed request",
	})
	if err != nil {
		t.Fatalf("direct signed UpsertDocument() error = %v", err)
	}
	if _, err := nodeClient.DeleteDocument(ctx, nodeRegistry.Nodes(nil)[0], principal, "probe-delete", "engineering", "transport-probe"); err != nil {
		t.Fatalf("direct signed DeleteDocument() error = %v", err)
	}
	api, err := NewAPI(nodeRegistry, executor, queryPlanner, nodeClient, StaticAuthenticator{Principal: principal})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	created := requestJSON(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/api/documents", map[string]any{
		"collection_id": "engineering",
		"document_id":   "recovery-runbook",
		"title":         "Replica recovery runbook",
		"content":       "Recover a replica by replaying committed log entries or installing a snapshot.",
	}, http.StatusCreated)
	if created["committed_offset"] != float64(probeOffset+2) {
		t.Fatalf("create response = %v", created)
	}

	searched := requestJSON(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/api/search", map[string]any{
		"query": "replica committed log", "mode": "auto", "collection_ids": []string{"engineering"},
	}, http.StatusOK)
	results, ok := searched["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("search response = %v", searched)
	}
	result := results[0].(map[string]any)
	if result["document_id"] != "recovery-runbook" || result["node_id"] != "node-a" {
		t.Fatalf("search result = %v", result)
	}

	document := requestJSON(
		t, httpServer.Client(), http.MethodGet,
		httpServer.URL+"/api/documents/engineering/recovery-runbook", nil, http.StatusOK,
	)
	if document["content"] != "Recover a replica by replaying committed log entries or installing a snapshot." {
		t.Fatalf("document response = %v", document)
	}

	requestJSON(
		t, httpServer.Client(), http.MethodDelete,
		httpServer.URL+"/api/documents/engineering/recovery-runbook", nil, http.StatusOK,
	)
	searched = requestJSON(t, httpServer.Client(), http.MethodPost, httpServer.URL+"/api/search", map[string]any{
		"query": "replica committed log", "mode": "lexical", "collection_ids": []string{"engineering"},
	}, http.StatusOK)
	results, ok = searched["results"].([]any)
	if !ok || len(results) != 0 {
		t.Fatalf("search after delete = %v", searched)
	}
}

func requestJSON(
	t *testing.T,
	client *http.Client,
	method, endpoint string,
	body any,
	wantStatus int,
) map[string]any {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, endpoint, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var decoded map[string]any
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; response=%v", method, endpoint, response.StatusCode, wantStatus, decoded)
	}
	return decoded
}
