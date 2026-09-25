package vector

import (
	"container/heap"
	"errors"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
)

const maximumLevel = 16

// HNSWOptions controls graph connectivity and search breadth.
type HNSWOptions struct {
	MaxConnections int
	EFConstruction int
	EFSearch       int
}

type graphNode struct {
	id        string
	vector    []float32
	level     int
	neighbors [][]string
}

// HNSW is a deterministic hierarchical proximity graph for cosine search.
type HNSW struct {
	mu             sync.RWMutex
	dimensions     int
	maxConnections int
	efConstruction int
	efSearch       int
	nodes          map[string]*graphNode
	entryID        string
	highestLevel   int
}

// NewHNSW returns an empty proximity graph.
func NewHNSW(dimensions int, options HNSWOptions) (*HNSW, error) {
	if dimensions <= 0 {
		return nil, errors.New("vector dimensions must be positive")
	}
	if options.MaxConnections == 0 {
		options.MaxConnections = 16
	}
	if options.EFConstruction == 0 {
		options.EFConstruction = 100
	}
	if options.EFSearch == 0 {
		options.EFSearch = 40
	}
	if options.MaxConnections < 2 {
		return nil, errors.New("maximum connections must be at least two")
	}
	if options.EFConstruction < options.MaxConnections {
		return nil, errors.New("construction breadth must cover maximum connections")
	}
	if options.EFSearch < 1 {
		return nil, errors.New("search breadth must be positive")
	}
	return &HNSW{
		dimensions:     dimensions,
		maxConnections: options.MaxConnections,
		efConstruction: options.EFConstruction,
		efSearch:       options.EFSearch,
		nodes:          make(map[string]*graphNode),
	}, nil
}

// Add inserts an item into the graph.
func (h *HNSW) Add(item Item) error {
	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		return ErrVectorIDRequired
	}
	values, err := normalize(item.Vector, h.dimensions)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.nodes[item.ID]; exists {
		return ErrDuplicateID
	}
	level := levelForID(item.ID)
	node := &graphNode{
		id:        item.ID,
		vector:    values,
		level:     level,
		neighbors: make([][]string, level+1),
	}
	if len(h.nodes) == 0 {
		h.nodes[item.ID] = node
		h.entryID = item.ID
		h.highestLevel = level
		return nil
	}

	entryID := h.entryID
	for currentLevel := h.highestLevel; currentLevel > level; currentLevel-- {
		entryID = h.greedySearch(values, entryID, currentLevel)
	}

	h.nodes[item.ID] = node
	connectionLevel := min(level, h.highestLevel)
	for currentLevel := connectionLevel; currentLevel >= 0; currentLevel-- {
		candidates := h.searchLayer(values, []string{entryID}, h.efConstruction, currentLevel)
		connectionCount := min(h.maxConnections, len(candidates))
		for _, candidate := range candidates[:connectionCount] {
			node.neighbors[currentLevel] = append(node.neighbors[currentLevel], candidate.id)
			neighbor := h.nodes[candidate.id]
			neighbor.neighbors[currentLevel] = append(neighbor.neighbors[currentLevel], item.ID)
			h.prune(neighbor, currentLevel)
		}
		if len(candidates) > 0 {
			entryID = candidates[0].id
		}
	}

	if level > h.highestLevel {
		h.entryID = item.ID
		h.highestLevel = level
	}
	return nil
}

// Search returns approximate nearest neighbors ordered by cosine similarity.
func (h *HNSW) Search(query []float32, limit int) ([]Result, error) {
	if limit < 0 {
		return nil, errors.New("limit cannot be negative")
	}
	values, err := normalize(query, h.dimensions)
	if err != nil {
		return nil, err
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.nodes) == 0 {
		return []Result{}, nil
	}
	entryID := h.entryID
	for level := h.highestLevel; level > 0; level-- {
		entryID = h.greedySearch(values, entryID, level)
	}
	breadth := max(h.efSearch, limit)
	candidates := h.searchLayer(values, []string{entryID}, breadth, 0)
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	results := make([]Result, len(candidates))
	for position, candidate := range candidates {
		results[position] = Result{ID: candidate.id, Score: 1 - candidate.distance}
	}
	return results, nil
}

func (h *HNSW) greedySearch(query []float32, entryID string, level int) string {
	currentID := entryID
	currentDistance := 1 - cosine(query, h.nodes[currentID].vector)
	for {
		improved := false
		for _, neighborID := range h.nodes[currentID].neighbors[level] {
			distance := 1 - cosine(query, h.nodes[neighborID].vector)
			if distance < currentDistance || (distance == currentDistance && neighborID < currentID) {
				currentID = neighborID
				currentDistance = distance
				improved = true
			}
		}
		if !improved {
			return currentID
		}
	}
}

func (h *HNSW) searchLayer(query []float32, entries []string, breadth int, level int) []neighbor {
	visited := make(map[string]struct{}, breadth)
	candidates := &nearestHeap{}
	results := &furthestHeap{}
	heap.Init(candidates)
	heap.Init(results)
	for _, entryID := range entries {
		if _, ok := visited[entryID]; ok {
			continue
		}
		visited[entryID] = struct{}{}
		candidate := neighbor{id: entryID, distance: 1 - cosine(query, h.nodes[entryID].vector)}
		heap.Push(candidates, candidate)
		heap.Push(results, candidate)
	}

	for candidates.Len() > 0 {
		candidate := heap.Pop(candidates).(neighbor)
		if results.Len() >= breadth && !closer(candidate, (*results)[0]) {
			break
		}
		for _, neighborID := range h.nodes[candidate.id].neighbors[level] {
			if _, ok := visited[neighborID]; ok {
				continue
			}
			visited[neighborID] = struct{}{}
			current := neighbor{id: neighborID, distance: 1 - cosine(query, h.nodes[neighborID].vector)}
			if results.Len() < breadth || closer(current, (*results)[0]) {
				heap.Push(candidates, current)
				heap.Push(results, current)
				if results.Len() > breadth {
					heap.Pop(results)
				}
			}
		}
	}

	ordered := make([]neighbor, results.Len())
	copy(ordered, *results)
	sort.Slice(ordered, func(left, right int) bool {
		return closer(ordered[left], ordered[right])
	})
	return ordered
}

func (h *HNSW) prune(node *graphNode, level int) {
	neighbors := node.neighbors[level]
	sort.Slice(neighbors, func(left, right int) bool {
		leftNeighbor := neighbor{
			id:       neighbors[left],
			distance: 1 - cosine(node.vector, h.nodes[neighbors[left]].vector),
		}
		rightNeighbor := neighbor{
			id:       neighbors[right],
			distance: 1 - cosine(node.vector, h.nodes[neighbors[right]].vector),
		}
		return closer(leftNeighbor, rightNeighbor)
	})
	if len(neighbors) > h.maxConnections {
		neighbors = neighbors[:h.maxConnections]
	}
	node.neighbors[level] = neighbors
}

func levelForID(id string) int {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(id))
	value := hash.Sum64()
	level := 0
	for level < maximumLevel && value&3 == 0 {
		level++
		value >>= 2
	}
	return level
}

type neighbor struct {
	id       string
	distance float32
}

func closer(left, right neighbor) bool {
	if left.distance == right.distance {
		return left.id < right.id
	}
	return left.distance < right.distance
}

type nearestHeap []neighbor

func (h nearestHeap) Len() int { return len(h) }
func (h nearestHeap) Less(left, right int) bool {
	return closer(h[left], h[right])
}
func (h nearestHeap) Swap(left, right int) { h[left], h[right] = h[right], h[left] }
func (h *nearestHeap) Push(value any)      { *h = append(*h, value.(neighbor)) }
func (h *nearestHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

type furthestHeap []neighbor

func (h furthestHeap) Len() int { return len(h) }
func (h furthestHeap) Less(left, right int) bool {
	return closer(h[right], h[left])
}
func (h furthestHeap) Swap(left, right int) { h[left], h[right] = h[right], h[left] }
func (h *furthestHeap) Push(value any)      { *h = append(*h, value.(neighbor)) }
func (h *furthestHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}
