package wal

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ayansaiyad/privatemesh/internal/search"
	"github.com/ayansaiyad/privatemesh/internal/segment"
)

func TestLogCommitsAndReplaysMutations(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "documents.wal")
	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := log.Append(segment.Upsert(search.Document{ID: "doc-a", Body: "first"}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := log.Append(segment.Delete("doc-b"))
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 {
		t.Fatalf("sequences = (%d, %d), want (1, 2)", first, second)
	}

	entries, err := log.Replay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("uncommitted Replay() = %+v", entries)
	}
	if err := log.Commit(first); err != nil {
		t.Fatal(err)
	}
	entries, err = log.Replay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := entrySequences(entries); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("Replay() sequences = %v, want [1]", got)
	}
	if err := log.Commit(second); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = reopened.Close()
	}()
	entries, err = reopened.Replay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := entrySequences(entries); !reflect.DeepEqual(got, []uint64{1, 2}) {
		t.Fatalf("reopened Replay() sequences = %v, want [1 2]", got)
	}
}

func TestLogRecoversIncompleteTail(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "documents.wal")
	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := log.Append(segment.Upsert(search.Document{ID: "stable"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Commit(sequence); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, filePermissions) //nolint:gosec // Test path is inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0, 0, 0, 9}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = recovered.Close()
	}()
	entries, err := recovered.Replay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := entrySequences(entries); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("Replay() sequences = %v, want [1]", got)
	}
	next, err := recovered.Append(segment.Upsert(search.Document{ID: "next"}))
	if err != nil {
		t.Fatal(err)
	}
	if next != 2 {
		t.Fatalf("next sequence = %d, want 2", next)
	}
}

func TestLogDetectsCorruption(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "documents.wal")
	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := log.Append(segment.Upsert(search.Document{ID: "doc-a", Body: "content"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Commit(sequence); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(path) //nolint:gosec // Test path is inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	contents[headerSize+recordHeaderSize+9] ^= 0xff
	if err := os.WriteFile(path, contents, filePermissions); err != nil { //nolint:gosec // Test path is inside t.TempDir.
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrCorruptLog) {
		t.Fatalf("Open() error = %v, want %v", err, ErrCorruptLog)
	}
}

func TestLogRejectsOversizedRecordWithoutAllocatingPayload(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "documents.wal")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, filePermissions) //nolint:gosec // Test path is inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if err := writeHeader(file, 0); err != nil {
		t.Fatal(err)
	}
	recordHeader := make([]byte, recordHeaderSize)
	binary.BigEndian.PutUint32(recordHeader[:4], maxRecordSize+1)
	if _, err := file.Write(recordHeader); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(path); !errors.Is(err, ErrCorruptLog) {
		t.Fatalf("Open() error = %v, want %v", err, ErrCorruptLog)
	}
}

func TestLogTruncatesThroughCheckpoint(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "documents.wal")
	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"doc-a", "doc-b", "doc-c"} {
		if _, err := log.Append(segment.Upsert(search.Document{ID: id})); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Commit(3); err != nil {
		t.Fatal(err)
	}
	if err := log.Truncate(2); err != nil {
		t.Fatal(err)
	}
	entries, err := log.Replay(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := entrySequences(entries); !reflect.DeepEqual(got, []uint64{3}) {
		t.Fatalf("Replay() sequences = %v, want [3]", got)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = reopened.Close()
	}()
	next, err := reopened.Append(segment.Upsert(search.Document{ID: "doc-d"}))
	if err != nil {
		t.Fatal(err)
	}
	if next != 4 {
		t.Fatalf("next sequence = %d, want 4", next)
	}
}

func TestLogReplayIsIdempotent(t *testing.T) {
	t.Parallel()

	log, err := Open(filepath.Join(t.TempDir(), "documents.wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = log.Close()
	}()
	mutations := []segment.Mutation{
		segment.Upsert(search.Document{ID: "doc-a", Body: "old"}),
		segment.Upsert(search.Document{ID: "doc-a", Body: "new"}),
		segment.Upsert(search.Document{ID: "doc-b", Body: "temporary"}),
		segment.Delete("doc-b"),
	}
	for _, mutation := range mutations {
		if _, err := log.Append(mutation); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Commit(4); err != nil {
		t.Fatal(err)
	}
	entries, err := log.Replay(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	index := search.NewIndex()
	applyEntries(t, index, entries)
	applyEntries(t, index, entries)
	if index.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", index.Len())
	}
	document, ok := index.Get("doc-a")
	if !ok || document.Body != "new" {
		t.Fatalf("Get(doc-a) = (%+v, %v)", document, ok)
	}
	if _, ok := index.Get("doc-b"); ok {
		t.Fatal("deleted document was restored")
	}
}

func TestLogReplayHonorsCancellation(t *testing.T) {
	t.Parallel()

	log, err := Open(filepath.Join(t.TempDir(), "documents.wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = log.Close()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := log.Replay(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Replay() error = %v, want %v", err, context.Canceled)
	}
}

func applyEntries(t *testing.T, index *search.Index, entries []Entry) {
	t.Helper()
	for _, entry := range entries {
		if entry.Mutation.Deleted {
			index.Delete(entry.Mutation.Document.ID)
			continue
		}
		if err := index.Upsert(entry.Mutation.Document); err != nil {
			t.Fatal(err)
		}
	}
}

func entrySequences(entries []Entry) []uint64 {
	sequences := make([]uint64, len(entries))
	for position, entry := range entries {
		sequences[position] = entry.Sequence
	}
	return sequences
}
