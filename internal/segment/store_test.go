package segment

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ayansaiyad/privatemesh/internal/search"
)

func TestStorePublishesAndRestoresDocuments(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstGeneration, err := store.Publish([]Mutation{
		Upsert(search.Document{ID: "doc-a", Body: "first"}),
		Upsert(search.Document{ID: "doc-b", Body: "second"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	secondGeneration, err := store.Publish([]Mutation{
		Upsert(search.Document{ID: "doc-a", Body: "updated"}),
		Delete("doc-b"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstGeneration != 1 || secondGeneration != 2 {
		t.Fatalf("generations = (%d, %d), want (1, 2)", firstGeneration, secondGeneration)
	}

	documents, err := store.Documents()
	if err != nil {
		t.Fatal(err)
	}
	want := []search.Document{{ID: "doc-a", Body: "updated"}}
	if !reflect.DeepEqual(documents, want) {
		t.Fatalf("Documents() = %+v, want %+v", documents, want)
	}
}

func TestStorePublishesWithoutTemporaryFiles(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store, err := NewStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish([]Mutation{Upsert(search.Document{ID: "doc-1"})}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != segmentName(1) {
		t.Fatalf("segment directory entries = %v", entryNames(entries))
	}
}

func TestStoreRecoversInterruptedPublication(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store, err := NewStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish([]Mutation{Upsert(search.Document{ID: "stable"})}); err != nil {
		t.Fatal(err)
	}
	incompletePath := filepath.Join(directory, ".segment-interrupted.tmp")
	if err := os.WriteFile(incompletePath, []byte("incomplete"), 0o600); err != nil {
		t.Fatal(err)
	}

	recovered, err := NewStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(incompletePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary segment still exists: %v", err)
	}
	documents, err := recovered.Documents()
	if err != nil {
		t.Fatal(err)
	}
	if got := documentIDs(documents); !reflect.DeepEqual(got, []string{"stable"}) {
		t.Fatalf("document IDs = %v", got)
	}
}

func TestStoreCompactsSegments(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mutations := [][]Mutation{
		{
			Upsert(search.Document{ID: "doc-a", Body: "old"}),
			Upsert(search.Document{ID: "doc-b", Body: "deleted later"}),
		},
		{
			Upsert(search.Document{ID: "doc-a", Body: "new"}),
			Delete("doc-b"),
		},
		{Upsert(search.Document{ID: "doc-c", Body: "current"})},
	}
	for _, batch := range mutations {
		if _, err := store.Publish(batch); err != nil {
			t.Fatal(err)
		}
	}

	compacted, err := store.Compact(2)
	if err != nil {
		t.Fatal(err)
	}
	if !compacted {
		t.Fatal("Compact() = false, want true")
	}
	segments, err := store.listLocked()
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].segment.generation != 4 {
		t.Fatalf("segments after compaction = %+v", segments)
	}
	documents, err := store.Documents()
	if err != nil {
		t.Fatal(err)
	}
	want := []search.Document{
		{ID: "doc-a", Body: "new"},
		{ID: "doc-c", Body: "current"},
	}
	if !reflect.DeepEqual(documents, want) {
		t.Fatalf("Documents() = %+v, want %+v", documents, want)
	}
}

func TestStoreUsesNewestCompleteSnapshot(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store, err := NewStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish([]Mutation{Upsert(search.Document{ID: "doc-a", Body: "old"})}); err != nil {
		t.Fatal(err)
	}
	if err := store.publishLocked(2, []record{{
		document: search.Document{ID: "doc-a", Body: "new"},
	}}); err != nil {
		t.Fatal(err)
	}

	recovered, err := NewStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	documents, err := recovered.Documents()
	if err != nil {
		t.Fatal(err)
	}
	want := []search.Document{{ID: "doc-a", Body: "new"}}
	if !reflect.DeepEqual(documents, want) {
		t.Fatalf("Documents() = %+v, want %+v", documents, want)
	}
}

func TestStoreReportsCorruptSegment(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store, err := NewStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish([]Mutation{Upsert(search.Document{ID: "doc-a"})}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, segmentName(1))
	// The path is contained within the test's temporary directory.
	//nolint:gosec
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents[len(contents)-1] ^= 0xff
	// The path is contained within the test's temporary directory.
	//nolint:gosec
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Documents(); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Documents() error = %v, want %v", err, ErrChecksumMismatch)
	}
}

func documentIDs(documents []search.Document) []string {
	ids := make([]string, len(documents))
	for position, document := range documents {
		ids[position] = document.ID
	}
	return ids
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for position, entry := range entries {
		names[position] = entry.Name()
	}
	return names
}
