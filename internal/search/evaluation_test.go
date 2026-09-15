package search

import (
	"encoding/json"
	"os"
	"testing"
)

type relevanceCorpus struct {
	Documents []Document       `json:"documents"`
	Queries   []relevanceQuery `json:"queries"`
}

type relevanceQuery struct {
	Text      string           `json:"text"`
	Judgments map[string]uint8 `json:"judgments"`
}

func TestRelevanceCorpus(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile("testdata/relevance.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus relevanceCorpus
	if err := json.Unmarshal(contents, &corpus); err != nil {
		t.Fatal(err)
	}

	index := NewIndex()
	for _, document := range corpus.Documents {
		if err := index.Upsert(document); err != nil {
			t.Fatal(err)
		}
	}

	for _, query := range corpus.Queries {
		query := query
		t.Run(query.Text, func(t *testing.T) {
			t.Parallel()
			results, err := index.Search(query.Text, MatchAny, 3)
			if err != nil {
				t.Fatal(err)
			}
			if score := NDCG(results, query.Judgments, 3); score < 0.85 {
				t.Fatalf("NDCG@3 = %.3f, want at least 0.850", score)
			}
		})
	}
}

func TestNDCG(t *testing.T) {
	t.Parallel()

	results := []Result{
		{Document: Document{ID: "best"}},
		{Document: Document{ID: "good"}},
		{Document: Document{ID: "irrelevant"}},
	}
	relevance := map[string]uint8{"best": 3, "good": 2}
	if score := NDCG(results, relevance, 3); score != 1 {
		t.Fatalf("NDCG() = %f, want 1", score)
	}

	reversed := []Result{results[1], results[0], results[2]}
	if score := NDCG(reversed, relevance, 3); score >= 1 || score <= 0 {
		t.Fatalf("NDCG() = %f, want a value between 0 and 1", score)
	}
}
