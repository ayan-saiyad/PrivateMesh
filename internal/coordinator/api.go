// Package coordinator provides the public HTTP API for distributed search.
package coordinator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	privatemeshv1 "github.com/ayansaiyad/privatemesh/gen/go/privatemesh/v1"
	"github.com/ayansaiyad/privatemesh/internal/distributed"
	"github.com/ayansaiyad/privatemesh/internal/identity"
	"github.com/ayansaiyad/privatemesh/internal/planner"
	"github.com/ayansaiyad/privatemesh/internal/registry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	maximumRequestBody   = 17 << 20
	defaultSearchLimit   = 10
	maximumSearchLimit   = 100
	defaultSearchTimeout = 1500 * time.Millisecond
	maximumSearchTimeout = 10 * time.Second
)

// Authenticator establishes the browser user's principal.
type Authenticator interface {
	Authenticate(request *http.Request) (identity.Principal, error)
}

// BearerAuthenticator verifies an OIDC access token.
type BearerAuthenticator struct {
	verifier *identity.Verifier
}

// NewBearerAuthenticator creates an HTTP bearer-token authenticator.
func NewBearerAuthenticator(verifier *identity.Verifier) (*BearerAuthenticator, error) {
	if verifier == nil {
		return nil, errors.New("OIDC token verifier is required")
	}
	return &BearerAuthenticator{verifier: verifier}, nil
}

// Authenticate verifies the request Authorization header.
func (a *BearerAuthenticator) Authenticate(request *http.Request) (identity.Principal, error) {
	scheme, token, found := strings.Cut(request.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return identity.Principal{}, errors.New("bearer token is required")
	}
	return a.verifier.Verify(request.Context(), strings.TrimSpace(token))
}

// StaticAuthenticator provides an explicitly configured local principal.
type StaticAuthenticator struct {
	Principal identity.Principal
}

// Authenticate returns the configured principal.
func (a StaticAuthenticator) Authenticate(_ *http.Request) (identity.Principal, error) {
	if strings.TrimSpace(a.Principal.Subject) == "" {
		return identity.Principal{}, errors.New("static principal is not configured")
	}
	return a.Principal, nil
}

// NodeClient performs authenticated operations on owning search nodes.
type NodeClient interface {
	distributed.NodeSearcher
	UpsertDocument(ctx context.Context, node registry.Node, principal identity.Principal, requestID, collectionID string, document *privatemeshv1.Document) (uint64, error)
	DeleteDocument(ctx context.Context, node registry.Node, principal identity.Principal, requestID, collectionID, documentID string) (uint64, error)
	GetDocument(ctx context.Context, node registry.Node, principal identity.Principal, requestID, collectionID, documentID string) (*privatemeshv1.GetDocumentResponse, error)
}

// API serves browser requests through the distributed executor.
type API struct {
	registry      *registry.Registry
	executor      *distributed.Executor
	planner       *planner.Planner
	nodes         NodeClient
	authenticator Authenticator
	newRequestID  func() (string, error)
}

// NewAPI creates a coordinator HTTP API.
func NewAPI(
	nodeRegistry *registry.Registry,
	executor *distributed.Executor,
	queryPlanner *planner.Planner,
	nodes NodeClient,
	authenticator Authenticator,
) (*API, error) {
	if nodeRegistry == nil || executor == nil || queryPlanner == nil || nodes == nil || authenticator == nil {
		return nil, errors.New("registry, executor, planner, node client, and authenticator are required")
	}
	return &API{
		registry: nodeRegistry, executor: executor, planner: queryPlanner,
		nodes: nodes, authenticator: authenticator, newRequestID: randomRequestID,
	}, nil
}

// RegisterRoutes adds API handlers to a mux.
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/search", a.search)
	mux.HandleFunc("GET /api/nodes", a.listNodes)
	mux.HandleFunc("POST /api/documents", a.upsertDocument)
	mux.HandleFunc("DELETE /api/documents/{collection_id}/{document_id}", a.deleteDocument)
	mux.HandleFunc("GET /api/documents/{collection_id}/{document_id}", a.getDocument)
}

type searchRequest struct {
	Query         string   `json:"query"`
	Mode          string   `json:"mode"`
	Limit         int      `json:"limit"`
	CollectionIDs []string `json:"collection_ids"`
	TimeoutMS     int      `json:"timeout_ms"`
}

type searchResponse struct {
	RequestID           string            `json:"request_id"`
	Mode                string            `json:"mode"`
	Results             []distributed.Hit `json:"results"`
	SearchedShardIDs    []string          `json:"searched_shard_ids"`
	UnavailableShardIDs []string          `json:"unavailable_shard_ids"`
}

func (a *API) search(response http.ResponseWriter, request *http.Request) {
	principal, ok := a.authenticate(response, request)
	if !ok {
		return
	}
	var input searchRequest
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	input.Query = strings.TrimSpace(input.Query)
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	if input.Query == "" {
		writeError(response, http.StatusBadRequest, "query is required")
		return
	}
	if input.Limit == 0 {
		input.Limit = defaultSearchLimit
	}
	if input.Limit < 1 || input.Limit > maximumSearchLimit {
		writeError(response, http.StatusBadRequest, "limit must be between 1 and 100")
		return
	}
	timeout := defaultSearchTimeout
	if input.TimeoutMS != 0 {
		timeout = time.Duration(input.TimeoutMS) * time.Millisecond
	}
	if timeout <= 0 || timeout > maximumSearchTimeout {
		writeError(response, http.StatusBadRequest, "timeout must be between 1 and 10000 milliseconds")
		return
	}
	nodes := a.registry.Nodes(input.CollectionIDs)
	if len(nodes) == 0 {
		writeError(response, http.StatusServiceUnavailable, "no search nodes are available")
		return
	}
	mode, err := a.selectMode(input.Mode, input.Query, timeout, nodes)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	requestID, err := a.newRequestID()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "could not create request ID")
		return
	}
	updates, err := a.executor.Execute(request.Context(), distributed.Query{
		RequestID: requestID, Text: input.Query, Mode: mode, Limit: input.Limit,
		CollectionIDs: input.CollectionIDs, Timeout: timeout, Principal: principal,
	})
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	var final distributed.Update
	for update := range updates {
		final = update
	}
	writeJSON(response, http.StatusOK, searchResponse{
		RequestID: final.RequestID, Mode: mode, Results: nonNilHits(final.Results),
		SearchedShardIDs: final.SearchedShardIDs, UnavailableShardIDs: final.UnavailableShardIDs,
	})
}

type documentRequest struct {
	CollectionID string `json:"collection_id"`
	DocumentID   string `json:"document_id"`
	Title        string `json:"title"`
	Content      string `json:"content"`
	MediaType    string `json:"media_type"`
}

func (a *API) upsertDocument(response http.ResponseWriter, request *http.Request) {
	principal, ok := a.authenticate(response, request)
	if !ok {
		return
	}
	var input documentRequest
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	input.CollectionID = strings.TrimSpace(input.CollectionID)
	input.DocumentID = strings.TrimSpace(input.DocumentID)
	if input.CollectionID == "" || input.DocumentID == "" {
		writeError(response, http.StatusBadRequest, "collection_id and document_id are required")
		return
	}
	node, ok := a.owningNode(response, input.CollectionID)
	if !ok {
		return
	}
	requestID, err := a.newRequestID()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "could not create request ID")
		return
	}
	offset, err := a.nodes.UpsertDocument(request.Context(), node, principal, requestID, input.CollectionID, &privatemeshv1.Document{
		DocumentId: input.DocumentID, Title: input.Title, Content: input.Content, MediaType: input.MediaType,
	})
	if err != nil {
		writeRPCError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"request_id": requestID, "node_id": node.ID, "committed_offset": offset,
	})
}

func (a *API) deleteDocument(response http.ResponseWriter, request *http.Request) {
	principal, ok := a.authenticate(response, request)
	if !ok {
		return
	}
	collectionID := strings.TrimSpace(request.PathValue("collection_id"))
	documentID := strings.TrimSpace(request.PathValue("document_id"))
	node, ok := a.owningNode(response, collectionID)
	if !ok {
		return
	}
	requestID, err := a.newRequestID()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "could not create request ID")
		return
	}
	offset, err := a.nodes.DeleteDocument(request.Context(), node, principal, requestID, collectionID, documentID)
	if err != nil {
		writeRPCError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"request_id": requestID, "node_id": node.ID, "committed_offset": offset,
	})
}

func (a *API) getDocument(response http.ResponseWriter, request *http.Request) {
	principal, ok := a.authenticate(response, request)
	if !ok {
		return
	}
	collectionID := strings.TrimSpace(request.PathValue("collection_id"))
	documentID := strings.TrimSpace(request.PathValue("document_id"))
	node, ok := a.owningNode(response, collectionID)
	if !ok {
		return
	}
	requestID, err := a.newRequestID()
	if err != nil {
		writeError(response, http.StatusInternalServerError, "could not create request ID")
		return
	}
	document, err := a.nodes.GetDocument(request.Context(), node, principal, requestID, collectionID, documentID)
	if err != nil {
		writeRPCError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"request_id": requestID, "document_id": document.GetDocumentId(),
		"media_type": document.GetMediaType(), "content": string(document.GetContent()),
	})
}

func (a *API) listNodes(response http.ResponseWriter, request *http.Request) {
	if _, ok := a.authenticate(response, request); !ok {
		return
	}
	type nodeResponse struct {
		ID               string            `json:"id"`
		CollectionIDs    []string          `json:"collection_ids"`
		Labels           map[string]string `json:"labels,omitempty"`
		AppliedLogOffset uint64            `json:"applied_log_offset"`
	}
	nodes := a.registry.Nodes(nil)
	result := make([]nodeResponse, len(nodes))
	for index, node := range nodes {
		result[index] = nodeResponse{
			ID: node.ID, CollectionIDs: node.CollectionIDs, Labels: node.Labels, AppliedLogOffset: node.AppliedLogOffset,
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"nodes": result})
}

func (a *API) selectMode(requested, query string, timeout time.Duration, nodes []registry.Node) (string, error) {
	if requested == "" {
		requested = "auto"
	}
	if requested != "auto" {
		if requested != "lexical" && requested != "vector" && requested != "hybrid" {
			return "", errors.New("mode must be auto, lexical, vector, or hybrid")
		}
		return requested, nil
	}
	vectorReady := 0
	for _, node := range nodes {
		if node.Labels["vector"] != "disabled" {
			vectorReady++
		}
	}
	plan, err := a.planner.Select(planner.Request{
		Query: query, Deadline: timeout,
		Health: planner.Health{TotalNodes: len(nodes), AvailableNodes: len(nodes), VectorReadyNodes: vectorReady},
	})
	if err != nil {
		return "", err
	}
	return string(plan.Strategy), nil
}

func (a *API) owningNode(response http.ResponseWriter, collectionID string) (registry.Node, bool) {
	if collectionID == "" {
		writeError(response, http.StatusBadRequest, "collection ID is required")
		return registry.Node{}, false
	}
	nodes := a.registry.Nodes([]string{collectionID})
	if len(nodes) == 0 {
		writeError(response, http.StatusServiceUnavailable, "collection has no available owner")
		return registry.Node{}, false
	}
	return nodes[0], true
}

func (a *API) authenticate(response http.ResponseWriter, request *http.Request) (identity.Principal, bool) {
	principal, err := a.authenticator.Authenticate(request)
	if err != nil {
		writeError(response, http.StatusUnauthorized, "authentication required")
		return identity.Principal{}, false
	}
	return principal, true
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(response, request.Body, maximumRequestBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("request body must be valid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeRPCError(response http.ResponseWriter, err error) {
	switch status.Code(err) {
	case codes.InvalidArgument:
		writeError(response, http.StatusBadRequest, status.Convert(err).Message())
	case codes.Unauthenticated:
		writeError(response, http.StatusUnauthorized, "authentication required")
	case codes.PermissionDenied:
		writeError(response, http.StatusForbidden, "permission denied")
	case codes.NotFound:
		writeError(response, http.StatusNotFound, "document not found")
	case codes.ResourceExhausted:
		writeError(response, http.StatusRequestEntityTooLarge, "document is too large")
	case codes.OK, codes.Canceled, codes.Unknown, codes.DeadlineExceeded, codes.AlreadyExists,
		codes.FailedPrecondition, codes.Aborted, codes.OutOfRange, codes.Unimplemented,
		codes.Internal, codes.Unavailable, codes.DataLoss:
		writeError(response, http.StatusBadGateway, "search node request failed")
	}
}

func writeError(response http.ResponseWriter, code int, message string) {
	writeJSON(response, code, map[string]string{"error": message})
}

func writeJSON(response http.ResponseWriter, code int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(code)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		return
	}
}

func randomRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate request ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func nonNilHits(hits []distributed.Hit) []distributed.Hit {
	if hits == nil {
		return []distributed.Hit{}
	}
	return hits
}
