package search

import (
	"errors"
	"reflect"
	"testing"
)

func TestIndexSearch(t *testing.T) {
	t.Parallel()

	index := NewIndex()
	documents := []Document{
		{ID: "doc-c", Title: "Search clusters", Body: "Queries cross node boundaries."},
		{ID: "doc-a", Title: "Distributed search", Body: "Private private documents stay local."},
		{ID: "doc-b", Title: "Local ownership", Body: "Private documents remain on their node."},
	}
	for _, document := range documents {
		if err := index.Upsert(document); err != nil {
			t.Fatalf("Upsert(%q) error = %v", document.ID, err)
		}
	}

	tests := []struct {
		name  string
		query string
		mode  MatchMode
		limit int
		want  []string
	}{
		{name: "all terms", query: "private search", mode: MatchAll, want: []string{"doc-a"}},
		{name: "any term", query: "search ownership", mode: MatchAny, want: []string{"doc-b", "doc-a", "doc-c"}},
		{name: "duplicate query terms", query: "private private", mode: MatchAll, want: []string{"doc-a", "doc-b"}},
		{name: "stable limit", query: "documents", mode: MatchAny, limit: 1, want: []string{"doc-a"}},
		{name: "no match", query: "missing", mode: MatchAny, want: []string{}},
		{name: "empty query", query: " --- ", mode: MatchAll, want: []string{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := index.Search(test.query, test.mode, test.limit)
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if ids := documentIDs(got); !reflect.DeepEqual(ids, test.want) {
				t.Fatalf("Search() IDs = %v, want %v", ids, test.want)
			}
		})
	}
}

func TestIndexSearchWeightsTitles(t *testing.T) {
	t.Parallel()

	index := NewIndex()
	documents := []Document{
		{ID: "body-hit", Title: "Reference", Body: "mesh"},
		{ID: "title-hit", Title: "Mesh", Body: "Reference"},
	}
	for _, document := range documents {
		if err := index.Upsert(document); err != nil {
			t.Fatal(err)
		}
	}

	results, err := index.Search("mesh", MatchAny, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ids := documentIDs(results); !reflect.DeepEqual(ids, []string{"title-hit", "body-hit"}) {
		t.Fatalf("Search() IDs = %v, want [title-hit body-hit]", ids)
	}
	if results[0].Score <= results[1].Score {
		t.Fatalf("title score %f is not greater than body score %f", results[0].Score, results[1].Score)
	}
}

func TestIndexSearchBreaksTiesByDocumentID(t *testing.T) {
	t.Parallel()

	index := NewIndex()
	for _, id := range []string{"doc-b", "doc-a"} {
		if err := index.Upsert(Document{ID: id, Title: "same"}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := index.Search("same", MatchAny, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ids := documentIDs(results); !reflect.DeepEqual(ids, []string{"doc-a", "doc-b"}) {
		t.Fatalf("Search() IDs = %v, want [doc-a doc-b]", ids)
	}
}

func TestIndexUpsertReplacesTerms(t *testing.T) {
	t.Parallel()

	index := NewIndex()
	if err := index.Upsert(Document{ID: "doc-1", Body: "old content"}); err != nil {
		t.Fatal(err)
	}
	if err := index.Upsert(Document{ID: "doc-1", Body: "new content"}); err != nil {
		t.Fatal(err)
	}

	oldMatches, err := index.Search("old", MatchAny, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(oldMatches) != 0 {
		t.Fatalf("Search(%q) returned %v after replacement", "old", documentIDs(oldMatches))
	}

	newMatches, err := index.Search("new", MatchAny, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ids := documentIDs(newMatches); !reflect.DeepEqual(ids, []string{"doc-1"}) {
		t.Fatalf("Search(%q) IDs = %v, want [doc-1]", "new", ids)
	}
}

func TestIndexDelete(t *testing.T) {
	t.Parallel()

	index := NewIndex()
	if err := index.Upsert(Document{ID: "doc-1", Body: "searchable"}); err != nil {
		t.Fatal(err)
	}
	if !index.Delete("doc-1") {
		t.Fatal("Delete() = false, want true")
	}
	if index.Delete("doc-1") {
		t.Fatal("second Delete() = true, want false")
	}
	if index.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", index.Len())
	}
	if _, ok := index.Get("doc-1"); ok {
		t.Fatal("Get() found deleted document")
	}

	matches, err := index.Search("searchable", MatchAny, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("Search() returned deleted document: %v", documentIDs(matches))
	}
}

func TestIndexAcceptsEmptyDocument(t *testing.T) {
	t.Parallel()

	var index Index
	if err := index.Upsert(Document{ID: "empty"}); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if index.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", index.Len())
	}
	if document, ok := index.Get("empty"); !ok || document.ID != "empty" {
		t.Fatalf("Get() = (%+v, %v), want empty document", document, ok)
	}
}

func TestIndexValidatesInput(t *testing.T) {
	t.Parallel()

	index := NewIndex()
	if err := index.Upsert(Document{ID: "  "}); !errors.Is(err, ErrDocumentIDRequired) {
		t.Fatalf("Upsert() error = %v, want %v", err, ErrDocumentIDRequired)
	}
	if _, err := index.Search("query", MatchMode(99), 0); !errors.Is(err, ErrInvalidMatchMode) {
		t.Fatalf("Search() mode error = %v, want %v", err, ErrInvalidMatchMode)
	}
	if _, err := index.Search("query", MatchAny, -1); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("Search() limit error = %v, want %v", err, ErrInvalidLimit)
	}
}

func documentIDs(results []Result) []string {
	ids := make([]string, len(results))
	for position, result := range results {
		ids[position] = result.Document.ID
	}
	return ids
}
