package replication

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ayansaiyad/privatemesh/internal/search"
	"github.com/ayansaiyad/privatemesh/internal/segment"
)

// ErrQuorumUnavailable indicates that a mutation could not reach a majority of replicas.
var ErrQuorumUnavailable = errors.New("replication quorum unavailable")

// Entry is a mutation at a shard log offset.
type Entry struct {
	Offset   uint64
	Mutation segment.Mutation
}

// Status reports a replica's applied and committed offsets.
type Status struct {
	AppliedOffset   uint64
	CommittedOffset uint64
}

// Snapshot is a complete shard image at a committed offset.
type Snapshot struct {
	Offset    uint64
	Documents []search.Document
}

// Replica stores replicated entries and snapshots.
type Replica interface {
	Append(ctx context.Context, entry Entry) error
	Commit(ctx context.Context, offset uint64) error
	Rollback(ctx context.Context, offset uint64) error
	Status(ctx context.Context) (Status, error)
	Entries(ctx context.Context, after uint64) ([]Entry, bool, error)
	Snapshot(ctx context.Context) (Snapshot, error)
	InstallSnapshot(ctx context.Context, snapshot Snapshot) error
}

// Primary replicates serialized writes while it owns a leadership lease.
type Primary struct {
	mu       sync.Mutex
	nodeID   string
	term     uint64
	election *Election
	local    Replica
	peers    []Replica
}

// NewPrimary creates a primary for one shard replica set.
func NewPrimary(nodeID string, term uint64, election *Election, local Replica, peers []Replica) (*Primary, error) {
	if election == nil {
		return nil, errors.New("election is required")
	}
	if local == nil {
		return nil, errors.New("local replica is required")
	}
	for _, peer := range peers {
		if peer == nil {
			return nil, errors.New("replica peers cannot be nil")
		}
	}
	if err := election.Validate(nodeID, term); err != nil {
		return nil, err
	}
	return &Primary{
		nodeID:   nodeID,
		term:     term,
		election: election,
		local:    local,
		peers:    append([]Replica(nil), peers...),
	}, nil
}

// Write appends and commits a mutation after a majority acknowledges it.
func (p *Primary) Write(ctx context.Context, mutation segment.Mutation) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.election.Validate(p.nodeID, p.term); err != nil {
		return 0, err
	}
	status, err := p.local.Status(ctx)
	if err != nil {
		return 0, fmt.Errorf("read local replica status: %w", err)
	}
	if status.AppliedOffset == ^uint64(0) {
		return 0, errors.New("replication offset exhausted")
	}
	entry := Entry{Offset: status.AppliedOffset + 1, Mutation: mutation}
	if err := p.local.Append(ctx, entry); err != nil {
		return 0, fmt.Errorf("append local replica: %w", err)
	}

	type acknowledgment struct {
		replica Replica
		err     error
	}
	acknowledgments := make(chan acknowledgment, len(p.peers))
	for _, peer := range p.peers {
		go func(replica Replica) {
			acknowledgments <- acknowledgment{replica: replica, err: replica.Append(ctx, entry)}
		}(peer)
	}
	acknowledged := []Replica{p.local}
	for range p.peers {
		acknowledgment := <-acknowledgments
		if acknowledgment.err == nil {
			acknowledged = append(acknowledged, acknowledgment.replica)
		}
	}
	quorum := (len(p.peers)+1)/2 + 1
	if len(acknowledged) < quorum {
		for _, replica := range acknowledged {
			_ = replica.Rollback(context.Background(), entry.Offset)
		}
		return 0, ErrQuorumUnavailable
	}

	if err := p.local.Commit(ctx, entry.Offset); err != nil {
		return 0, fmt.Errorf("commit local replica: %w", err)
	}
	for _, replica := range acknowledged[1:] {
		_ = replica.Commit(ctx, entry.Offset)
	}
	return entry.Offset, nil
}

// CatchUp brings a replica to the source's committed offset using entries or a snapshot.
func CatchUp(ctx context.Context, source, target Replica) error {
	if source == nil || target == nil {
		return errors.New("source and target replicas are required")
	}
	sourceStatus, err := source.Status(ctx)
	if err != nil {
		return fmt.Errorf("read source status: %w", err)
	}
	targetStatus, err := target.Status(ctx)
	if err != nil {
		return fmt.Errorf("read target status: %w", err)
	}
	if targetStatus.CommittedOffset > sourceStatus.CommittedOffset {
		return errors.New("target commit is ahead of the source commit")
	}
	if targetStatus.CommittedOffset == sourceStatus.CommittedOffset {
		return nil
	}
	if targetStatus.AppliedOffset > targetStatus.CommittedOffset {
		if err := target.Rollback(ctx, targetStatus.CommittedOffset+1); err != nil {
			return fmt.Errorf("roll back target entries: %w", err)
		}
	}

	entries, available, err := source.Entries(ctx, targetStatus.CommittedOffset)
	if err != nil {
		return fmt.Errorf("read source entries: %w", err)
	}
	if !available {
		snapshot, err := source.Snapshot(ctx)
		if err != nil {
			return fmt.Errorf("read source snapshot: %w", err)
		}
		if err := target.InstallSnapshot(ctx, snapshot); err != nil {
			return fmt.Errorf("install target snapshot: %w", err)
		}
		return nil
	}

	for _, entry := range entries {
		if entry.Offset > sourceStatus.CommittedOffset {
			break
		}
		if err := target.Append(ctx, entry); err != nil {
			return fmt.Errorf("append target entry: %w", err)
		}
	}
	if err := target.Commit(ctx, sourceStatus.CommittedOffset); err != nil {
		return fmt.Errorf("commit target entries: %w", err)
	}
	return nil
}
