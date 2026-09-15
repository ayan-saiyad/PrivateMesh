// Package rpcclient calls search nodes and signs the forwarded principal.
package rpcclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	privatemeshv1 "github.com/ayansaiyad/privatemesh/gen/go/privatemesh/v1"
	"github.com/ayansaiyad/privatemesh/internal/distributed"
	"github.com/ayansaiyad/privatemesh/internal/identity"
	"github.com/ayansaiyad/privatemesh/internal/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Client pools search-node connections and signs each request principal.
type Client struct {
	mu          sync.Mutex
	signer      *identity.Signer
	dialOptions []grpc.DialOption
	connections map[string]*grpc.ClientConn
}

// New creates a search-node client. Additional dial options can replace local transport defaults.
func New(signer *identity.Signer, dialOptions ...grpc.DialOption) (*Client, error) {
	if signer == nil {
		return nil, errors.New("principal signer is required")
	}
	if len(dialOptions) == 0 {
		dialOptions = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	return &Client{
		signer: signer, dialOptions: append([]grpc.DialOption(nil), dialOptions...),
		connections: make(map[string]*grpc.ClientConn),
	}, nil
}

// Search executes a query on one registered node.
func (c *Client) Search(ctx context.Context, node registry.Node, query distributed.Query) ([]distributed.Hit, error) {
	client, err := c.searchClient(node.Address)
	if err != nil {
		return nil, err
	}
	ctx, err = c.principalContext(ctx, query.Principal)
	if err != nil {
		return nil, err
	}
	mode, err := retrievalMode(query.Mode)
	if err != nil {
		return nil, err
	}
	request := &privatemeshv1.SearchRequest{
		RequestId: query.RequestID, Query: query.Text, Mode: mode,
		Limit:         uint32(query.Limit), //nolint:gosec // Query limits are validated by the coordinator API.
		CollectionIds: query.CollectionIDs,
	}
	if query.Timeout > 0 {
		request.LatencyBudget = durationpb.New(query.Timeout)
	}
	stream, err := client.Search(ctx, request)
	if err != nil {
		return nil, err
	}
	var hits []distributed.Hit
	for {
		response, receiveErr := stream.Recv()
		if errors.Is(receiveErr, io.EOF) {
			return hits, nil
		}
		if receiveErr != nil {
			return nil, receiveErr
		}
		hits = make([]distributed.Hit, len(response.GetResults()))
		for index, result := range response.GetResults() {
			hits[index] = distributed.Hit{
				DocumentID: result.GetDocumentId(), CollectionID: result.GetCollectionId(), NodeID: result.GetNodeId(),
				Title: result.GetTitle(), Snippet: result.GetSnippet(), Score: result.GetScore(), Rank: int(result.GetRank()),
			}
		}
	}
}

// UpsertDocument writes a document to one owning node.
func (c *Client) UpsertDocument(
	ctx context.Context,
	node registry.Node,
	principal identity.Principal,
	requestID, collectionID string,
	document *privatemeshv1.Document,
) (uint64, error) {
	client, err := c.indexClient(node.Address)
	if err != nil {
		return 0, err
	}
	ctx, err = c.principalContext(ctx, principal)
	if err != nil {
		return 0, err
	}
	response, err := client.UpsertDocument(ctx, &privatemeshv1.UpsertDocumentRequest{
		RequestId: requestID, CollectionId: collectionID, Document: document,
	})
	if err != nil {
		return 0, err
	}
	return response.GetCommittedOffset(), nil
}

// DeleteDocument removes a document from one owning node.
func (c *Client) DeleteDocument(
	ctx context.Context,
	node registry.Node,
	principal identity.Principal,
	requestID, collectionID, documentID string,
) (uint64, error) {
	client, err := c.indexClient(node.Address)
	if err != nil {
		return 0, err
	}
	ctx, err = c.principalContext(ctx, principal)
	if err != nil {
		return 0, err
	}
	response, err := client.DeleteDocument(ctx, &privatemeshv1.DeleteDocumentRequest{
		RequestId: requestID, CollectionId: collectionID, DocumentId: documentID,
	})
	if err != nil {
		return 0, err
	}
	return response.GetCommittedOffset(), nil
}

// GetDocument retrieves a full document from one owning node.
func (c *Client) GetDocument(
	ctx context.Context,
	node registry.Node,
	principal identity.Principal,
	requestID, collectionID, documentID string,
) (*privatemeshv1.GetDocumentResponse, error) {
	client, err := c.searchClient(node.Address)
	if err != nil {
		return nil, err
	}
	ctx, err = c.principalContext(ctx, principal)
	if err != nil {
		return nil, err
	}
	return client.GetDocument(ctx, &privatemeshv1.GetDocumentRequest{
		RequestId: requestID, CollectionId: collectionID, DocumentId: documentID,
	})
}

// Close closes all pooled node connections.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var closeErr error
	for address, connection := range c.connections {
		if err := connection.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close connection %s: %w", address, err))
		}
	}
	c.connections = make(map[string]*grpc.ClientConn)
	return closeErr
}

func (c *Client) searchClient(address string) (privatemeshv1.SearchServiceClient, error) {
	connection, err := c.connection(address)
	if err != nil {
		return nil, err
	}
	return privatemeshv1.NewSearchServiceClient(connection), nil
}

func (c *Client) indexClient(address string) (privatemeshv1.IndexServiceClient, error) {
	connection, err := c.connection(address)
	if err != nil {
		return nil, err
	}
	return privatemeshv1.NewIndexServiceClient(connection), nil
}

func (c *Client) connection(address string) (*grpc.ClientConn, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, errors.New("search node address is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if connection := c.connections[address]; connection != nil {
		return connection, nil
	}
	connection, err := grpc.NewClient(address, c.dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("create search node client: %w", err)
	}
	c.connections[address] = connection
	return connection, nil
}

func (c *Client) principalContext(ctx context.Context, principal identity.Principal) (context.Context, error) {
	token, err := c.signer.Sign(principal)
	if err != nil {
		return nil, err
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token), nil
}

func retrievalMode(mode string) (privatemeshv1.RetrievalMode, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "lexical":
		return privatemeshv1.RetrievalMode_RETRIEVAL_MODE_LEXICAL, nil
	case "vector":
		return privatemeshv1.RetrievalMode_RETRIEVAL_MODE_VECTOR, nil
	case "hybrid":
		return privatemeshv1.RetrievalMode_RETRIEVAL_MODE_HYBRID, nil
	case "auto":
		return privatemeshv1.RetrievalMode_RETRIEVAL_MODE_AUTO, nil
	default:
		return privatemeshv1.RetrievalMode_RETRIEVAL_MODE_UNSPECIFIED, errors.New("unsupported retrieval mode")
	}
}

var _ distributed.NodeSearcher = (*Client)(nil)
var _ io.Closer = (*Client)(nil)
