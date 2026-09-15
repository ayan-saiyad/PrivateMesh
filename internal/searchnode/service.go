package searchnode

import (
	"context"
	"errors"
	"io"
	"strings"

	privatemeshv1 "github.com/ayansaiyad/privatemesh/gen/go/privatemesh/v1"
	"github.com/ayansaiyad/privatemesh/internal/authorization"
	"github.com/ayansaiyad/privatemesh/internal/identity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	maximumResultLimit = 100
	maximumContentSize = 16 << 20
)

// Authenticator verifies the signed principal attached to an RPC.
type Authenticator interface {
	Authenticate(ctx context.Context) (identity.Principal, error)
}

// TokenAuthenticator verifies bearer tokens from gRPC metadata.
type TokenAuthenticator struct {
	verifier *identity.Verifier
}

// NewTokenAuthenticator creates a signed metadata authenticator.
func NewTokenAuthenticator(verifier *identity.Verifier) (*TokenAuthenticator, error) {
	if verifier == nil {
		return nil, errors.New("principal token verifier is required")
	}
	return &TokenAuthenticator{verifier: verifier}, nil
}

// Authenticate validates an authorization metadata value.
func (a *TokenAuthenticator) Authenticate(ctx context.Context) (identity.Principal, error) {
	values := metadata.ValueFromIncomingContext(ctx, "authorization")
	if len(values) != 1 {
		return identity.Principal{}, errors.New("one authorization value is required")
	}
	scheme, token, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return identity.Principal{}, errors.New("bearer token is required")
	}
	return a.verifier.Verify(ctx, strings.TrimSpace(token))
}

// Service exposes an owning collection through gRPC.
type Service struct {
	privatemeshv1.UnimplementedSearchServiceServer
	privatemeshv1.UnimplementedIndexServiceServer
	nodeID        string
	engine        *Engine
	authenticator Authenticator
}

// NewService creates a search and indexing service.
func NewService(nodeID string, engine *Engine, authenticator Authenticator) (*Service, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || engine == nil || authenticator == nil {
		return nil, errors.New("node ID, engine, and authenticator are required")
	}
	return &Service{nodeID: nodeID, engine: engine, authenticator: authenticator}, nil
}

// Register adds search and indexing RPCs to a gRPC server.
func (s *Service) Register(server grpc.ServiceRegistrar) {
	privatemeshv1.RegisterSearchServiceServer(server, s)
	privatemeshv1.RegisterIndexServiceServer(server, s)
}

// Search returns locally authorized results from the owned collection.
func (s *Service) Search(
	request *privatemeshv1.SearchRequest,
	stream grpc.ServerStreamingServer[privatemeshv1.SearchResponse],
) error {
	if request == nil {
		return status.Error(codes.InvalidArgument, "search request is required")
	}
	principal, err := s.authenticator.Authenticate(stream.Context())
	if err != nil {
		return status.Error(codes.Unauthenticated, "valid principal token is required")
	}
	if !servesCollection(s.engine.CollectionID(), request.GetCollectionIds()) {
		return stream.Send(&privatemeshv1.SearchResponse{RequestId: request.GetRequestId(), Complete: true})
	}
	mode, err := retrievalMode(request.GetMode())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	limit := int(request.GetLimit())
	if limit <= 0 || limit > maximumResultLimit {
		return status.Errorf(codes.InvalidArgument, "result limit must be between 1 and %d", maximumResultLimit)
	}
	hits, err := s.engine.Search(stream.Context(), request.GetRequestId(), request.GetQuery(), mode, limit, principal)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	results := make([]*privatemeshv1.SearchResult, len(hits))
	for index, hit := range hits {
		results[index] = &privatemeshv1.SearchResult{
			DocumentId:   hit.DocumentID,
			CollectionId: s.engine.CollectionID(),
			NodeId:       s.nodeID,
			Title:        hit.Title,
			Snippet:      hit.Snippet,
			Score:        hit.Score,
			Rank:         uint32(index + 1), //nolint:gosec // Result limits are bounded above.
		}
	}
	return stream.Send(&privatemeshv1.SearchResponse{
		RequestId: request.GetRequestId(), Results: results,
		SearchedShardIds: []string{s.nodeID + ":" + s.engine.CollectionID()}, Complete: true,
	})
}

// GetDocument returns an authorized full document.
func (s *Service) GetDocument(ctx context.Context, request *privatemeshv1.GetDocumentRequest) (*privatemeshv1.GetDocumentResponse, error) {
	if request == nil || request.GetCollectionId() != s.engine.CollectionID() {
		return nil, status.Error(codes.NotFound, "document not found")
	}
	principal, err := s.authenticator.Authenticate(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "valid principal token is required")
	}
	document, found, err := s.engine.Get(ctx, request.GetRequestId(), request.GetDocumentId(), principal)
	if errors.Is(err, authorization.ErrPermissionDenied) {
		return nil, status.Error(codes.PermissionDenied, "document access denied")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "document lookup failed")
	}
	if !found {
		return nil, status.Error(codes.NotFound, "document not found")
	}
	return &privatemeshv1.GetDocumentResponse{
		DocumentId: document.ID, MediaType: document.MediaType, Content: []byte(document.Content),
	}, nil
}

// UpsertDocument durably adds or replaces a document.
func (s *Service) UpsertDocument(ctx context.Context, request *privatemeshv1.UpsertDocumentRequest) (*privatemeshv1.UpsertDocumentResponse, error) {
	principal, err := s.authenticator.Authenticate(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "valid principal token is required")
	}
	if !hasScope(principal, "documents.write") {
		return nil, status.Error(codes.PermissionDenied, "documents.write scope is required")
	}
	if request == nil || request.GetCollectionId() != s.engine.CollectionID() || request.GetDocument() == nil {
		return nil, status.Error(codes.InvalidArgument, "matching collection and document are required")
	}
	if len(request.GetDocument().GetContent()) > maximumContentSize {
		return nil, status.Error(codes.ResourceExhausted, "document content is too large")
	}
	offset, err := s.engine.Upsert(ctx, Document{
		ID: request.GetDocument().GetDocumentId(), Title: request.GetDocument().GetTitle(),
		Content: request.GetDocument().GetContent(), MediaType: request.GetDocument().GetMediaType(),
	})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &privatemeshv1.UpsertDocumentResponse{CommittedOffset: offset}, nil
}

// DeleteDocument durably removes a document.
func (s *Service) DeleteDocument(ctx context.Context, request *privatemeshv1.DeleteDocumentRequest) (*privatemeshv1.DeleteDocumentResponse, error) {
	principal, err := s.authenticator.Authenticate(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "valid principal token is required")
	}
	if !hasScope(principal, "documents.write") {
		return nil, status.Error(codes.PermissionDenied, "documents.write scope is required")
	}
	if request == nil || request.GetCollectionId() != s.engine.CollectionID() {
		return nil, status.Error(codes.InvalidArgument, "matching collection is required")
	}
	offset, err := s.engine.Delete(ctx, request.GetDocumentId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &privatemeshv1.DeleteDocumentResponse{CommittedOffset: offset}, nil
}

func retrievalMode(mode privatemeshv1.RetrievalMode) (RetrievalMode, error) {
	switch mode {
	case privatemeshv1.RetrievalMode_RETRIEVAL_MODE_LEXICAL:
		return RetrievalLexical, nil
	case privatemeshv1.RetrievalMode_RETRIEVAL_MODE_VECTOR:
		return RetrievalVector, nil
	case privatemeshv1.RetrievalMode_RETRIEVAL_MODE_HYBRID,
		privatemeshv1.RetrievalMode_RETRIEVAL_MODE_AUTO:
		return RetrievalHybrid, nil
	case privatemeshv1.RetrievalMode_RETRIEVAL_MODE_UNSPECIFIED:
		return 0, errors.New("retrieval mode is required")
	default:
		return 0, errors.New("unsupported retrieval mode")
	}
}

func servesCollection(collectionID string, requested []string) bool {
	if len(requested) == 0 {
		return true
	}
	for _, candidate := range requested {
		if strings.TrimSpace(candidate) == collectionID {
			return true
		}
	}
	return false
}

func hasScope(principal identity.Principal, scope string) bool {
	for _, candidate := range principal.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

var _ io.Closer = (*Engine)(nil)
