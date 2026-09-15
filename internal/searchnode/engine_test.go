package searchnode

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ayansaiyad/privatemesh/internal/authorization"
	"github.com/ayansaiyad/privatemesh/internal/embedding"
	"github.com/ayansaiyad/privatemesh/internal/identity"
)

func TestEngineSearchesFiltersAndRecovers(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	authorizer := testAuthorizer(t)
	engine := openTestEngine(t, directory, authorizer)
	documents := []Document{
		{ID: "runbook", Title: "Replica recovery", Content: "Recover replicas from committed log entries after process failure."},
		{ID: "design", Title: "Design guide", Content: "Typography and visual hierarchy for product interfaces."},
		{ID: "restricted", Title: "Private incident", Content: "Restricted production incident details."},
	}
	for _, document := range documents {
		if _, err := engine.Upsert(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}

	principal := identity.Principal{Subject: "alice", Groups: []string{"engineering"}, Scopes: []string{"search.execute", "documents.read"}}
	for _, mode := range []RetrievalMode{RetrievalLexical, RetrievalVector, RetrievalHybrid} {
		hits, err := engine.Search(context.Background(), "request-1", "replica recovery failure", mode, 10, principal)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 || hits[0].DocumentID != "runbook" {
			t.Fatalf("Search(mode=%d) = %+v, want runbook first", mode, hits)
		}
		for _, hit := range hits {
			if hit.DocumentID == "restricted" {
				t.Fatalf("Search(mode=%d) returned restricted document", mode)
			}
		}
	}

	if _, _, err := engine.Get(context.Background(), "request-2", "restricted", principal); !errors.Is(err, authorization.ErrPermissionDenied) {
		t.Fatalf("Get(restricted) error = %v, want permission denied", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}

	recovered := openTestEngine(t, directory, authorizer)
	t.Cleanup(func() { _ = recovered.Close() })
	if recovered.Offset() != uint64(len(documents)) {
		t.Fatalf("recovered offset = %d, want %d", recovered.Offset(), len(documents))
	}
	hits, err := recovered.Search(context.Background(), "request-3", "replica", RetrievalLexical, 10, principal)
	if err != nil {
		t.Fatal(err)
	}
	if ids := hitIDs(hits); !reflect.DeepEqual(ids, []string{"runbook"}) {
		t.Fatalf("recovered Search() IDs = %v, want [runbook]", ids)
	}
}

func TestEngineDeletesDocumentsDurably(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	authorizer := testAuthorizer(t)
	engine := openTestEngine(t, directory, authorizer)
	if _, err := engine.Upsert(context.Background(), Document{ID: "doc-a", Content: "searchable"}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Delete(context.Background(), "doc-a"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	recovered := openTestEngine(t, directory, authorizer)
	t.Cleanup(func() { _ = recovered.Close() })
	hits, err := recovered.Search(
		context.Background(), "request-1", "searchable", RetrievalLexical, 10,
		identity.Principal{Subject: "alice", Groups: []string{"engineering"}, Scopes: []string{"search.execute"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("Search() returned deleted documents: %+v", hits)
	}
}

func openTestEngine(t *testing.T, directory string, authorizer *authorization.Enforcer) *Engine {
	t.Helper()
	embedder, err := embedding.NewHash(128)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := Open(context.Background(), Options{
		CollectionID: "engineering", DataDirectory: directory, Dimensions: 128,
		Embedder: embedder, Authorizer: authorizer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func testAuthorizer(t *testing.T) *authorization.Enforcer {
	t.Helper()
	store := authorization.NewMemoryStore()
	if err := store.SetCollectionPolicy("engineering", authorization.Policy{
		Version: "1",
		Rules: []authorization.Rule{{
			Effect:  authorization.EffectAllow,
			Actions: []authorization.Action{authorization.ActionSearch, authorization.ActionRead},
			Groups:  []string{"engineering"},
			Scopes:  []string{"search.execute"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDocumentPolicy("engineering", "restricted", authorization.Policy{
		Version: "1",
		Rules: []authorization.Rule{{
			Effect:  authorization.EffectAllow,
			Actions: []authorization.Action{authorization.ActionSearch, authorization.ActionRead},
			Groups:  []string{"incident-commanders"},
			Scopes:  []string{"search.execute"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	authorizer, err := authorization.NewEnforcer(store, &authorization.MemoryAuditSink{}, []byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return authorizer
}

func hitIDs(hits []Hit) []string {
	ids := make([]string, len(hits))
	for index, hit := range hits {
		ids[index] = hit.DocumentID
	}
	return ids
}
