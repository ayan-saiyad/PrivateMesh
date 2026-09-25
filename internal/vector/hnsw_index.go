package vector

import (
	"sort"
	"strings"
	"sync"
)

// SearchIndex is the mutable vector-search contract used by a search node.
//
// Implementations must make a successful Upsert visible to subsequent Search
// calls and keep replacement and deletion semantics safe for durable document
// updates.
type SearchIndex interface {
	Upsert(Item) error
	Delete(id string) bool
	Search(query []float32, limit int) ([]Result, error)
}

// HNSWIndex makes the immutable-insertion HNSW graph safe for document
// replacement and deletion. It retains the source vectors and atomically
// rebuilds a deterministic graph after a mutation, so a search always sees a
// complete graph rather than a partially edited neighborhood.
//
// The rebuild tradeoff is intentional for the current node-sized collections:
// it preserves correct durable upsert/delete behavior while search requests use
// approximate HNSW nearest-neighbor traversal.
type HNSWIndex struct {
	mu         sync.RWMutex
	dimensions int
	options    HNSWOptions
	vectors    map[string][]float32
	graph      *HNSW
}

// NewHNSWIndex creates a mutable, approximate HNSW search index.
func NewHNSWIndex(dimensions int, options HNSWOptions) (*HNSWIndex, error) {
	graph, err := NewHNSW(dimensions, options)
	if err != nil {
		return nil, err
	}
	return &HNSWIndex{
		dimensions: dimensions,
		options:    options,
		vectors:    make(map[string][]float32),
		graph:      graph,
	}, nil
}

// Upsert adds an item or replaces the vector associated with its ID.
func (i *HNSWIndex) Upsert(item Item) error {
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		return ErrVectorIDRequired
	}
	normalized, err := normalize(item.Vector, i.dimensions)
	if err != nil {
		return err
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	next := cloneVectors(i.vectors)
	next[item.ID] = normalized
	graph, err := rebuildHNSW(i.dimensions, i.options, next)
	if err != nil {
		return err
	}
	i.vectors = next
	i.graph = graph
	return nil
}

// Delete removes an item and reports whether it existed.
func (i *HNSWIndex) Delete(id string) bool {
	id = strings.TrimSpace(id)
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, ok := i.vectors[id]; !ok {
		return false
	}
	next := cloneVectors(i.vectors)
	delete(next, id)
	graph, err := rebuildHNSW(i.dimensions, i.options, next)
	if err != nil {
		// The existing graph remains usable. rebuildHNSW only fails for invalid
		// vectors, which Upsert rejects before storing them.
		return false
	}
	i.vectors = next
	i.graph = graph
	return true
}

// Search returns approximate nearest neighbors ordered by cosine similarity.
func (i *HNSWIndex) Search(query []float32, limit int) ([]Result, error) {
	i.mu.RLock()
	graph := i.graph
	i.mu.RUnlock()
	return graph.Search(query, limit)
}

func rebuildHNSW(dimensions int, options HNSWOptions, vectors map[string][]float32) (*HNSW, error) {
	graph, err := NewHNSW(dimensions, options)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(vectors))
	for id := range vectors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := graph.Add(Item{ID: id, Vector: vectors[id]}); err != nil {
			return nil, err
		}
	}
	return graph, nil
}

func cloneVectors(values map[string][]float32) map[string][]float32 {
	cloned := make(map[string][]float32, len(values))
	for id, vector := range values {
		cloned[id] = append([]float32(nil), vector...)
	}
	return cloned
}

var _ SearchIndex = (*ExactIndex)(nil)
var _ SearchIndex = (*HNSWIndex)(nil)
