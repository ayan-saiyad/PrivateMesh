package vector

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// ExactIndex compares a query with every stored vector.
type ExactIndex struct {
	mu         sync.RWMutex
	dimensions int
	vectors    map[string][]float32
}

// NewExactIndex returns an empty exact index.
func NewExactIndex(dimensions int) (*ExactIndex, error) {
	if dimensions <= 0 {
		return nil, errors.New("vector dimensions must be positive")
	}
	return &ExactIndex{
		dimensions: dimensions,
		vectors:    make(map[string][]float32),
	}, nil
}

// Upsert adds an item or replaces the vector associated with its ID.
func (i *ExactIndex) Upsert(item Item) error {
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		return errors.New("vector ID is required")
	}
	normalized, err := normalize(item.Vector, i.dimensions)
	if err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.vectors[item.ID] = normalized
	return nil
}

// Delete removes an item and reports whether it existed.
func (i *ExactIndex) Delete(id string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	id = strings.TrimSpace(id)
	if _, ok := i.vectors[id]; !ok {
		return false
	}
	delete(i.vectors, id)
	return true
}

// Search returns the nearest items ordered by cosine similarity.
func (i *ExactIndex) Search(query []float32, limit int) ([]Result, error) {
	if limit < 0 {
		return nil, errors.New("limit cannot be negative")
	}
	normalized, err := normalize(query, i.dimensions)
	if err != nil {
		return nil, err
	}

	i.mu.RLock()
	defer i.mu.RUnlock()
	results := make([]Result, 0, len(i.vectors))
	for id, values := range i.vectors {
		results = append(results, Result{ID: id, Score: cosine(normalized, values)})
	}
	sortResults(results)
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func sortResults(results []Result) {
	sort.Slice(results, func(left, right int) bool {
		if results[left].Score == results[right].Score {
			return results[left].ID < results[right].ID
		}
		return results[left].Score > results[right].Score
	})
}
