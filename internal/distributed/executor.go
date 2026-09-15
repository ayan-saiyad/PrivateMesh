// Package distributed coordinates searches across active nodes.
package distributed

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/ayansaiyad/privatemesh/internal/identity"
	"github.com/ayansaiyad/privatemesh/internal/registry"
)

const reciprocalRankConstant = 60

// Query is a search request sent to selected nodes.
type Query struct {
	RequestID     string
	Text          string
	Mode          string
	Limit         int
	CollectionIDs []string
	Timeout       time.Duration
	Principal     identity.Principal
}

// Hit is a document match returned by a search node.
type Hit struct {
	DocumentID   string  `json:"document_id"`
	CollectionID string  `json:"collection_id"`
	NodeID       string  `json:"node_id"`
	Title        string  `json:"title"`
	Snippet      string  `json:"snippet"`
	Score        float64 `json:"score"`
	Rank         int     `json:"rank"`
}

// Update contains the best merged results and current shard coverage.
type Update struct {
	RequestID           string
	Results             []Hit
	SearchedShardIDs    []string
	UnavailableShardIDs []string
	Complete            bool
}

// Catalog selects active nodes for requested collections.
type Catalog interface {
	Nodes(collectionIDs []string) []registry.Node
}

// NodeSearcher executes a query on one search node.
type NodeSearcher interface {
	Search(ctx context.Context, node registry.Node, query Query) ([]Hit, error)
}

// Executor fans queries out and merges results as nodes respond.
type Executor struct {
	catalog  Catalog
	searcher NodeSearcher
}

// NewExecutor creates a distributed query executor.
func NewExecutor(catalog Catalog, searcher NodeSearcher) (*Executor, error) {
	if catalog == nil {
		return nil, errors.New("node catalog is required")
	}
	if searcher == nil {
		return nil, errors.New("node searcher is required")
	}
	return &Executor{catalog: catalog, searcher: searcher}, nil
}

// Execute streams cumulative results until every selected node responds or the context ends.
func (e *Executor) Execute(parent context.Context, query Query) (<-chan Update, error) {
	query.RequestID = strings.TrimSpace(query.RequestID)
	query.Text = strings.TrimSpace(query.Text)
	if query.RequestID == "" {
		return nil, errors.New("request ID is required")
	}
	if query.Text == "" {
		return nil, errors.New("query text is required")
	}
	if query.Limit <= 0 {
		return nil, errors.New("result limit must be positive")
	}
	if query.Timeout < 0 {
		return nil, errors.New("query timeout cannot be negative")
	}
	query.CollectionIDs = normalizedCollectionIDs(query.CollectionIDs)

	ctx := parent
	cancel := func() {}
	if query.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, query.Timeout)
	}
	nodes := e.catalog.Nodes(query.CollectionIDs)
	updates := make(chan Update, len(nodes)+1)
	go e.execute(ctx, cancel, query, nodes, updates)
	return updates, nil
}

type nodeResponse struct {
	node registry.Node
	hits []Hit
	err  error
}

func (e *Executor) execute(
	ctx context.Context,
	cancel context.CancelFunc,
	query Query,
	nodes []registry.Node,
	updates chan<- Update,
) {
	defer close(updates)
	defer cancel()
	responses := make(chan nodeResponse, len(nodes))
	pending := make(map[string]registry.Node, len(nodes))
	for _, node := range nodes {
		pending[node.ID] = node
		go func(node registry.Node) {
			hits, err := e.searcher.Search(ctx, node, query)
			select {
			case responses <- nodeResponse{node: node, hits: hits, err: err}:
			case <-ctx.Done():
			}
		}(node)
	}

	state := mergeState{
		hits:        make(map[string]Hit),
		scores:      make(map[string]float64),
		searched:    make(map[string]struct{}),
		unavailable: make(map[string]struct{}),
	}
	if len(nodes) == 0 {
		updates <- state.update(query, true)
		return
	}

	for len(pending) > 0 {
		select {
		case response := <-responses:
			if _, ok := pending[response.node.ID]; !ok {
				continue
			}
			delete(pending, response.node.ID)
			if response.err != nil {
				state.markUnavailable(response.node, query.CollectionIDs)
			} else {
				state.merge(response.node, response.hits, query.CollectionIDs)
			}
			updates <- state.update(query, len(pending) == 0)
		case <-ctx.Done():
			for _, node := range pending {
				state.markUnavailable(node, query.CollectionIDs)
			}
			updates <- state.update(query, true)
			return
		}
	}
}

type mergeState struct {
	hits        map[string]Hit
	scores      map[string]float64
	searched    map[string]struct{}
	unavailable map[string]struct{}
}

func (s *mergeState) merge(node registry.Node, hits []Hit, requestedCollections []string) {
	for _, shardID := range shardIDs(node, requestedCollections) {
		s.searched[shardID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(hits))
	for position, hit := range hits {
		key := hit.CollectionID + "\x00" + hit.DocumentID
		if hit.DocumentID == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if hit.NodeID == "" {
			hit.NodeID = node.ID
		}
		current, exists := s.hits[key]
		if !exists || hit.NodeID < current.NodeID {
			s.hits[key] = hit
		}
		s.scores[key] += 1 / float64(reciprocalRankConstant+position+1)
	}
}

func (s *mergeState) markUnavailable(node registry.Node, requestedCollections []string) {
	for _, shardID := range shardIDs(node, requestedCollections) {
		s.unavailable[shardID] = struct{}{}
	}
}

func (s *mergeState) update(query Query, complete bool) Update {
	results := make([]Hit, 0, len(s.hits))
	for key, hit := range s.hits {
		hit.Score = s.scores[key]
		results = append(results, hit)
	}
	sort.Slice(results, func(left, right int) bool {
		if results[left].Score == results[right].Score {
			if results[left].CollectionID == results[right].CollectionID {
				return results[left].DocumentID < results[right].DocumentID
			}
			return results[left].CollectionID < results[right].CollectionID
		}
		return results[left].Score > results[right].Score
	})
	if len(results) > query.Limit {
		results = results[:query.Limit]
	}
	for position := range results {
		results[position].Rank = position + 1
	}
	return Update{
		RequestID:           query.RequestID,
		Results:             results,
		SearchedShardIDs:    sortedKeys(s.searched),
		UnavailableShardIDs: sortedKeys(s.unavailable),
		Complete:            complete,
	}
}

func shardIDs(node registry.Node, requestedCollections []string) []string {
	requested := make(map[string]struct{}, len(requestedCollections))
	for _, collectionID := range requestedCollections {
		requested[strings.TrimSpace(collectionID)] = struct{}{}
	}
	shards := make([]string, 0, len(node.CollectionIDs))
	for _, collectionID := range node.CollectionIDs {
		if len(requested) == 0 {
			shards = append(shards, node.ID+":"+collectionID)
			continue
		}
		if _, ok := requested[collectionID]; ok {
			shards = append(shards, node.ID+":"+collectionID)
		}
	}
	if len(shards) == 0 {
		shards = append(shards, node.ID)
	}
	sort.Strings(shards)
	return shards
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func normalizedCollectionIDs(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			unique[value] = struct{}{}
		}
	}
	return sortedKeys(unique)
}
