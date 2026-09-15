// Package replication coordinates durable writes across shard replicas.
package replication

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrNotLeader indicates that a node does not hold the current leadership lease.
var ErrNotLeader = errors.New("node does not hold the leadership lease")

// Leadership identifies the current primary and its fencing term.
type Leadership struct {
	NodeID    string
	Term      uint64
	ExpiresAt time.Time
}

// Election grants time-bounded, monotonically fenced leadership.
type Election struct {
	mu       sync.Mutex
	duration time.Duration
	now      func() time.Time
	current  Leadership
}

// NewElection creates an empty election state.
func NewElection(duration time.Duration) (*Election, error) {
	if duration <= 0 {
		return nil, errors.New("leadership lease duration must be positive")
	}
	return &Election{duration: duration, now: time.Now}, nil
}

// Campaign acquires leadership when the current lease is absent or expired.
func (e *Election) Campaign(nodeID string) (Leadership, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return Leadership{}, errors.New("node ID is required")
	}
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if now.Before(e.current.ExpiresAt) && e.current.NodeID != nodeID {
		return Leadership{}, ErrNotLeader
	}
	if e.current.NodeID != nodeID || !now.Before(e.current.ExpiresAt) {
		e.current.Term++
	}
	e.current.NodeID = nodeID
	e.current.ExpiresAt = now.Add(e.duration)
	return e.current, nil
}

// Renew extends a matching, unexpired leadership lease.
func (e *Election) Renew(nodeID string, term uint64) (Leadership, error) {
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.current.NodeID != strings.TrimSpace(nodeID) || e.current.Term != term || !now.Before(e.current.ExpiresAt) {
		return Leadership{}, ErrNotLeader
	}
	e.current.ExpiresAt = now.Add(e.duration)
	return e.current, nil
}

// Validate checks that a node and term still own the live lease.
func (e *Election) Validate(nodeID string, term uint64) error {
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.current.NodeID != strings.TrimSpace(nodeID) || e.current.Term != term || !now.Before(e.current.ExpiresAt) {
		return ErrNotLeader
	}
	return nil
}

// Current returns live leadership or an empty value after expiration.
func (e *Election) Current() Leadership {
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !now.Before(e.current.ExpiresAt) {
		return Leadership{}
	}
	return e.current
}
