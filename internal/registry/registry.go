// Package registry tracks active search nodes and their collection leases.
package registry

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNodeNotFound indicates that no active registration exists for a node.
	ErrNodeNotFound = errors.New("node registration not found")
	// ErrLeaseMismatch indicates that a request does not own the active node lease.
	ErrLeaseMismatch = errors.New("node lease does not match")
)

// Node describes a searchable process and the collections it owns.
type Node struct {
	ID               string
	Address          string
	CollectionIDs    []string
	Labels           map[string]string
	AppliedLogOffset uint64
}

// Lease identifies a time-bounded node registration.
type Lease struct {
	ID        string
	ExpiresAt time.Time
	Revision  uint64
}

type registration struct {
	node      Node
	leaseID   string
	expiresAt time.Time
}

// Registry maintains active node leases in memory.
type Registry struct {
	mu       sync.RWMutex
	ttl      time.Duration
	now      func() time.Time
	newID    func() (string, error)
	revision uint64
	nodes    map[string]registration
}

// New returns an empty node registry.
func New(ttl time.Duration) (*Registry, error) {
	if ttl <= 0 {
		return nil, errors.New("lease duration must be positive")
	}
	return &Registry{
		ttl:   ttl,
		now:   time.Now,
		newID: randomLeaseID,
		nodes: make(map[string]registration),
	}, nil
}

// Register creates a new lease for a node, replacing any previous registration.
func (r *Registry) Register(node Node) (Lease, error) {
	node.ID = strings.TrimSpace(node.ID)
	node.Address = strings.TrimSpace(node.Address)
	if node.ID == "" {
		return Lease{}, errors.New("node ID is required")
	}
	if node.Address == "" {
		return Lease{}, errors.New("node address is required")
	}
	leaseID, err := r.newID()
	if err != nil {
		return Lease{}, err
	}
	node.CollectionIDs = normalizedStrings(node.CollectionIDs)
	node.Labels = cloneLabels(node.Labels)
	now := r.now()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeExpiredLocked(now)
	r.revision++
	registration := registration{
		node:      node,
		leaseID:   leaseID,
		expiresAt: now.Add(r.ttl),
	}
	r.nodes[node.ID] = registration
	return Lease{ID: leaseID, ExpiresAt: registration.expiresAt, Revision: r.revision}, nil
}

// Heartbeat renews a node lease and records its latest applied log offset.
func (r *Registry) Heartbeat(nodeID, leaseID string, appliedLogOffset uint64) (Lease, error) {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeExpiredLocked(now)
	current, ok := r.nodes[strings.TrimSpace(nodeID)]
	if !ok {
		return Lease{}, ErrNodeNotFound
	}
	if current.leaseID != strings.TrimSpace(leaseID) {
		return Lease{}, ErrLeaseMismatch
	}
	current.node.AppliedLogOffset = appliedLogOffset
	current.expiresAt = now.Add(r.ttl)
	r.nodes[current.node.ID] = current
	return Lease{ID: current.leaseID, ExpiresAt: current.expiresAt, Revision: r.revision}, nil
}

// Deregister removes a node when the lease matches.
func (r *Registry) Deregister(nodeID, leaseID string) error {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeExpiredLocked(now)
	nodeID = strings.TrimSpace(nodeID)
	current, ok := r.nodes[nodeID]
	if !ok {
		return ErrNodeNotFound
	}
	if current.leaseID != strings.TrimSpace(leaseID) {
		return ErrLeaseMismatch
	}
	delete(r.nodes, nodeID)
	r.revision++
	return nil
}

// Nodes returns active nodes that own at least one requested collection.
func (r *Registry) Nodes(collectionIDs []string) []Node {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeExpiredLocked(now)
	requested := stringSet(collectionIDs)
	nodes := make([]Node, 0, len(r.nodes))
	for _, current := range r.nodes {
		if len(requested) > 0 && !intersects(current.node.CollectionIDs, requested) {
			continue
		}
		node := current.node
		node.CollectionIDs = append([]string(nil), node.CollectionIDs...)
		node.Labels = cloneLabels(node.Labels)
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(left, right int) bool {
		return nodes[left].ID < nodes[right].ID
	})
	return nodes
}

// Revision returns the current registry revision after expiring stale leases.
func (r *Registry) Revision() uint64 {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeExpiredLocked(now)
	return r.revision
}

func (r *Registry) removeExpiredLocked(now time.Time) {
	for nodeID, current := range r.nodes {
		if now.Before(current.expiresAt) {
			continue
		}
		delete(r.nodes, nodeID)
		r.revision++
	}
}

func normalizedStrings(values []string) []string {
	set := stringSet(values)
	normalized := make([]string, 0, len(set))
	for value := range set {
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func intersects(values []string, requested map[string]struct{}) bool {
	for _, value := range values {
		if _, ok := requested[value]; ok {
			return true
		}
	}
	return false
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}
	return cloned
}

func randomLeaseID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
