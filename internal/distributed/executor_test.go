package distributed

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ayansaiyad/privatemesh/internal/registry"
)

func TestExecutorStreamsPartialResults(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	searcher := &fakeSearcher{
		results: map[string][]Hit{
			"node-a": {{DocumentID: "a", CollectionID: "files", Title: "First"}},
			"node-b": {{DocumentID: "b", CollectionID: "files", Title: "Second"}},
		},
		blockedNode: "node-b",
		release:     release,
	}
	executor := newTestExecutor(t, searcher)
	updates, err := executor.Execute(context.Background(), Query{
		RequestID:     "request-1",
		Text:          "private search",
		Limit:         10,
		CollectionIDs: []string{"files"},
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case update := <-updates:
		if update.Complete || len(update.Results) != 1 || update.Results[0].DocumentID != "a" {
			t.Fatalf("first update = %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("first partial update was not streamed")
	}
	close(release)
	final := <-updates
	if !final.Complete || len(final.Results) != 2 {
		t.Fatalf("final update = %+v", final)
	}
	if got := final.SearchedShardIDs; !reflect.DeepEqual(got, []string{"node-a:files", "node-b:files"}) {
		t.Fatalf("searched shards = %v", got)
	}
}

func TestExecutorMergesGlobalTopResults(t *testing.T) {
	t.Parallel()

	searcher := &fakeSearcher{results: map[string][]Hit{
		"node-a": {
			{DocumentID: "lexical", CollectionID: "files"},
			{DocumentID: "shared", CollectionID: "files"},
		},
		"node-b": {
			{DocumentID: "semantic", CollectionID: "files"},
			{DocumentID: "shared", CollectionID: "files"},
		},
	}}
	executor := newTestExecutor(t, searcher)
	updates, err := executor.Execute(context.Background(), Query{
		RequestID: "request-1",
		Text:      "search",
		Limit:     2,
	})
	if err != nil {
		t.Fatal(err)
	}
	var final Update
	for update := range updates {
		final = update
	}
	if got := hitIDs(final.Results); !reflect.DeepEqual(got, []string{"shared", "lexical"}) {
		t.Fatalf("merged result IDs = %v", got)
	}
	if final.Results[0].Rank != 1 || final.Results[1].Rank != 2 {
		t.Fatalf("merged ranks = %+v", final.Results)
	}
}

func TestExecutorMarksTimedOutNodesUnavailable(t *testing.T) {
	t.Parallel()

	searcher := &fakeSearcher{blockedNode: "node-b", release: make(chan struct{})}
	executor := newTestExecutor(t, searcher)
	updates, err := executor.Execute(context.Background(), Query{
		RequestID: "request-1",
		Text:      "search",
		Limit:     5,
		Timeout:   20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	var final Update
	for update := range updates {
		final = update
	}
	if !final.Complete {
		t.Fatal("final update is not complete")
	}
	if got := final.UnavailableShardIDs; !reflect.DeepEqual(got, []string{"node-b:files"}) {
		t.Fatalf("unavailable shards = %v", got)
	}
}

func TestExecutorValidatesQueries(t *testing.T) {
	t.Parallel()

	executor := newTestExecutor(t, &fakeSearcher{})
	if _, err := executor.Execute(context.Background(), Query{Text: "query", Limit: 1}); err == nil {
		t.Fatal("Execute() accepted an empty request ID")
	}
	if _, err := executor.Execute(context.Background(), Query{RequestID: "id", Limit: 1}); err == nil {
		t.Fatal("Execute() accepted empty query text")
	}
}

type staticCatalog struct {
	nodes []registry.Node
}

func (c staticCatalog) Nodes(_ []string) []registry.Node {
	return append([]registry.Node(nil), c.nodes...)
}

type fakeSearcher struct {
	results     map[string][]Hit
	errors      map[string]error
	blockedNode string
	release     <-chan struct{}
}

func (s *fakeSearcher) Search(ctx context.Context, node registry.Node, _ Query) ([]Hit, error) {
	if node.ID == s.blockedNode {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := s.errors[node.ID]; err != nil {
		return nil, err
	}
	return append([]Hit(nil), s.results[node.ID]...), nil
}

func newTestExecutor(t *testing.T, searcher NodeSearcher) *Executor {
	t.Helper()
	executor, err := NewExecutor(staticCatalog{nodes: []registry.Node{
		{ID: "node-a", Address: "node-a:9000", CollectionIDs: []string{"files"}},
		{ID: "node-b", Address: "node-b:9000", CollectionIDs: []string{"files"}},
	}}, searcher)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func hitIDs(hits []Hit) []string {
	ids := make([]string, len(hits))
	for position, hit := range hits {
		ids[position] = hit.DocumentID
	}
	return ids
}
