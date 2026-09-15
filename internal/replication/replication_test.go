package replication

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ayansaiyad/privatemesh/internal/search"
	"github.com/ayansaiyad/privatemesh/internal/segment"
)

func TestElectionFencesExpiredLeaders(t *testing.T) {
	t.Parallel()

	election, err := NewElection(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	election.now = func() time.Time { return now }
	first, err := election.Campaign("node-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := election.Campaign("node-b"); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("concurrent Campaign() error = %v, want %v", err, ErrNotLeader)
	}
	now = now.Add(11 * time.Second)
	second, err := election.Campaign("node-b")
	if err != nil {
		t.Fatal(err)
	}
	if second.Term <= first.Term {
		t.Fatalf("new term = %d, want greater than %d", second.Term, first.Term)
	}
	if err := election.Validate("node-a", first.Term); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("old leader validation error = %v, want %v", err, ErrNotLeader)
	}
}

func TestPrimaryCommitsWithMajority(t *testing.T) {
	t.Parallel()

	election, leadership := activeElection(t, "node-a")
	local := newMemoryReplica()
	available := newMemoryReplica()
	unavailable := newMemoryReplica()
	unavailable.available = false
	primary, err := NewPrimary("node-a", leadership.Term, election, local, []Replica{available, unavailable})
	if err != nil {
		t.Fatal(err)
	}

	offset, err := primary.Write(context.Background(), segment.Upsert(search.Document{ID: "doc-a", Body: "replicated"}))
	if err != nil {
		t.Fatal(err)
	}
	if offset != 1 {
		t.Fatalf("Write() offset = %d, want 1", offset)
	}
	for _, replica := range []*memoryReplica{local, available} {
		status, err := replica.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.CommittedOffset != 1 {
			t.Fatalf("committed offset = %d, want 1", status.CommittedOffset)
		}
	}
}

func TestPrimaryRollsBackWithoutMajority(t *testing.T) {
	t.Parallel()

	election, leadership := activeElection(t, "node-a")
	local := newMemoryReplica()
	first := newMemoryReplica()
	first.available = false
	second := newMemoryReplica()
	second.available = false
	primary, err := NewPrimary("node-a", leadership.Term, election, local, []Replica{first, second})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := primary.Write(context.Background(), segment.Upsert(search.Document{ID: "doc-a"})); !errors.Is(err, ErrQuorumUnavailable) {
		t.Fatalf("Write() error = %v, want %v", err, ErrQuorumUnavailable)
	}
	status, err := local.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.AppliedOffset != 0 || status.CommittedOffset != 0 {
		t.Fatalf("local status after rollback = %+v", status)
	}
}

func TestPrimaryRejectsWritesAfterLeaseLoss(t *testing.T) {
	t.Parallel()

	election, leadership := activeElection(t, "node-a")
	now := election.now()
	primary, err := NewPrimary("node-a", leadership.Term, election, newMemoryReplica(), nil)
	if err != nil {
		t.Fatal(err)
	}
	election.now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, err := primary.Write(context.Background(), segment.Upsert(search.Document{ID: "doc-a"})); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Write() error = %v, want %v", err, ErrNotLeader)
	}
}

func TestCatchUpReplaysRetainedEntries(t *testing.T) {
	t.Parallel()

	source := newMemoryReplica()
	appendAndCommit(t, source,
		segment.Upsert(search.Document{ID: "doc-a", Body: "old"}),
		segment.Upsert(search.Document{ID: "doc-a", Body: "new"}),
		segment.Upsert(search.Document{ID: "doc-b", Body: "current"}),
	)
	target := newMemoryReplica()
	if err := CatchUp(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	assertReplicaState(t, target, 3, []search.Document{
		{ID: "doc-a", Body: "new"},
		{ID: "doc-b", Body: "current"},
	})
}

func TestCatchUpInstallsSnapshotWhenHistoryWasCompacted(t *testing.T) {
	t.Parallel()

	source := newMemoryReplica()
	appendAndCommit(t, source,
		segment.Upsert(search.Document{ID: "doc-a", Body: "current"}),
		segment.Upsert(search.Document{ID: "doc-b", Body: "deleted"}),
		segment.Delete("doc-b"),
	)
	source.historyFloor = 3
	target := newMemoryReplica()
	if err := CatchUp(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	assertReplicaState(t, target, 3, []search.Document{{ID: "doc-a", Body: "current"}})
}

func TestCatchUpReplacesAnUncommittedTail(t *testing.T) {
	t.Parallel()

	source := newMemoryReplica()
	appendAndCommit(t, source, segment.Upsert(search.Document{ID: "doc-a", Body: "committed"}))
	target := newMemoryReplica()
	if err := target.Append(context.Background(), Entry{
		Offset:   1,
		Mutation: segment.Upsert(search.Document{ID: "doc-a", Body: "abandoned"}),
	}); err != nil {
		t.Fatal(err)
	}
	if err := CatchUp(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	assertReplicaState(t, target, 1, []search.Document{{ID: "doc-a", Body: "committed"}})
}

func TestReplicaCatchesUpAfterProcessLoss(t *testing.T) {
	t.Parallel()

	election, leadership := activeElection(t, "node-a")
	local := newMemoryReplica()
	online := newMemoryReplica()
	restarting := newMemoryReplica()
	restarting.available = false
	primary, err := NewPrimary("node-a", leadership.Term, election, local, []Replica{online, restarting})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := primary.Write(context.Background(), segment.Upsert(search.Document{ID: "doc-a", Body: "during outage"})); err != nil {
		t.Fatal(err)
	}
	restarting.available = true
	if err := CatchUp(context.Background(), local, restarting); err != nil {
		t.Fatal(err)
	}
	assertReplicaState(t, restarting, 1, []search.Document{{ID: "doc-a", Body: "during outage"}})
}

func activeElection(t *testing.T, nodeID string) (*Election, Leadership) {
	t.Helper()
	election, err := NewElection(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	leadership, err := election.Campaign(nodeID)
	if err != nil {
		t.Fatal(err)
	}
	return election, leadership
}

type memoryReplica struct {
	mu              sync.Mutex
	available       bool
	entries         []Entry
	committedOffset uint64
	historyFloor    uint64
	documents       map[string]search.Document
}

func newMemoryReplica() *memoryReplica {
	return &memoryReplica{available: true, documents: make(map[string]search.Document)}
}

func (r *memoryReplica) Append(ctx context.Context, entry Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.available {
		return errors.New("replica unavailable")
	}
	expected := r.appliedOffsetLocked() + 1
	if entry.Offset != expected {
		return errors.New("entry offset is not contiguous")
	}
	r.entries = append(r.entries, entry)
	return nil
}

func (r *memoryReplica) Commit(ctx context.Context, offset uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.available {
		return errors.New("replica unavailable")
	}
	if offset < r.committedOffset || offset > r.appliedOffsetLocked() {
		return errors.New("invalid commit offset")
	}
	for _, entry := range r.entries {
		if entry.Offset <= r.committedOffset || entry.Offset > offset {
			continue
		}
		applyMutation(r.documents, entry.Mutation)
	}
	r.committedOffset = offset
	return nil
}

func (r *memoryReplica) Rollback(ctx context.Context, offset uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if offset <= r.committedOffset {
		return errors.New("cannot roll back a committed entry")
	}
	for len(r.entries) > 0 && r.entries[len(r.entries)-1].Offset >= offset {
		r.entries = r.entries[:len(r.entries)-1]
	}
	return nil
}

func (r *memoryReplica) Status(ctx context.Context) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.available {
		return Status{}, errors.New("replica unavailable")
	}
	return Status{AppliedOffset: r.appliedOffsetLocked(), CommittedOffset: r.committedOffset}, nil
}

func (r *memoryReplica) Entries(ctx context.Context, after uint64) ([]Entry, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.available {
		return nil, false, errors.New("replica unavailable")
	}
	if after < r.historyFloor {
		return nil, false, nil
	}
	entries := make([]Entry, 0)
	for _, entry := range r.entries {
		if entry.Offset > after {
			entries = append(entries, entry)
		}
	}
	return entries, true, nil
}

func (r *memoryReplica) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.available {
		return Snapshot{}, errors.New("replica unavailable")
	}
	documents := make([]search.Document, 0, len(r.documents))
	for _, document := range r.documents {
		documents = append(documents, document)
	}
	sort.Slice(documents, func(left, right int) bool { return documents[left].ID < documents[right].ID })
	return Snapshot{Offset: r.committedOffset, Documents: documents}, nil
}

func (r *memoryReplica) InstallSnapshot(ctx context.Context, snapshot Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.available {
		return errors.New("replica unavailable")
	}
	r.documents = make(map[string]search.Document, len(snapshot.Documents))
	for _, document := range snapshot.Documents {
		r.documents[document.ID] = document
	}
	r.entries = nil
	r.historyFloor = snapshot.Offset
	r.committedOffset = snapshot.Offset
	return nil
}

func (r *memoryReplica) appliedOffsetLocked() uint64 {
	if len(r.entries) == 0 {
		return r.historyFloor
	}
	return r.entries[len(r.entries)-1].Offset
}

func appendAndCommit(t *testing.T, replica *memoryReplica, mutations ...segment.Mutation) {
	t.Helper()
	for _, mutation := range mutations {
		status, err := replica.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		offset := status.AppliedOffset + 1
		if err := replica.Append(context.Background(), Entry{Offset: offset, Mutation: mutation}); err != nil {
			t.Fatal(err)
		}
	}
	status, err := replica.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := replica.Commit(context.Background(), status.AppliedOffset); err != nil {
		t.Fatal(err)
	}
}

func assertReplicaState(t *testing.T, replica *memoryReplica, offset uint64, want []search.Document) {
	t.Helper()
	snapshot, err := replica.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Offset != offset || !reflect.DeepEqual(snapshot.Documents, want) {
		t.Fatalf("Snapshot() = %+v, want offset %d documents %+v", snapshot, offset, want)
	}
}

func applyMutation(documents map[string]search.Document, mutation segment.Mutation) {
	if mutation.Deleted {
		delete(documents, mutation.Document.ID)
		return
	}
	documents[mutation.Document.ID] = mutation.Document
}
