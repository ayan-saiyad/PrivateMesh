package vector

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

func TestExactIndexSearch(t *testing.T) {
	t.Parallel()

	index, err := NewExactIndex(3)
	if err != nil {
		t.Fatal(err)
	}
	items := []Item{
		{ID: "east", Vector: []float32{1, 0, 0}},
		{ID: "north", Vector: []float32{0, 1, 0}},
		{ID: "near-east", Vector: []float32{0.9, 0.1, 0}},
	}
	for _, item := range items {
		if err := index.Upsert(item); err != nil {
			t.Fatal(err)
		}
	}
	results, err := index.Search([]float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultIDs(results); !reflect.DeepEqual(got, []string{"east", "near-east"}) {
		t.Fatalf("Search() IDs = %v", got)
	}
	if !index.Delete("east") || index.Delete("east") {
		t.Fatal("Delete() did not report existence correctly")
	}
}

func TestVectorValidation(t *testing.T) {
	t.Parallel()

	index, err := NewExactIndex(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Upsert(Item{ID: "short", Vector: []float32{1}}); !errors.Is(err, ErrDimensionMismatch) {
		t.Fatalf("Upsert() error = %v, want %v", err, ErrDimensionMismatch)
	}
	if err := index.Upsert(Item{ID: "zero", Vector: []float32{0, 0}}); !errors.Is(err, ErrInvalidVector) {
		t.Fatalf("Upsert() error = %v, want %v", err, ErrInvalidVector)
	}
}

func TestHNSWNearestNeighbors(t *testing.T) {
	t.Parallel()

	graph, err := NewHNSW(3, HNSWOptions{MaxConnections: 4, EFConstruction: 16, EFSearch: 12})
	if err != nil {
		t.Fatal(err)
	}
	items := []Item{
		{ID: "east", Vector: []float32{1, 0, 0}},
		{ID: "near-east", Vector: []float32{0.9, 0.1, 0}},
		{ID: "north", Vector: []float32{0, 1, 0}},
		{ID: "west", Vector: []float32{-1, 0, 0}},
		{ID: "south", Vector: []float32{0, -1, 0}},
	}
	for _, item := range items {
		if err := graph.Add(item); err != nil {
			t.Fatal(err)
		}
	}
	if err := graph.Add(items[0]); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate Add() error = %v, want %v", err, ErrDuplicateID)
	}
	results, err := graph.Search([]float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultIDs(results); !reflect.DeepEqual(got, []string{"east", "near-east"}) {
		t.Fatalf("Search() IDs = %v", got)
	}
}

func TestHNSWIndexUpsertsAndDeletes(t *testing.T) {
	t.Parallel()

	index, err := NewHNSWIndex(3, HNSWOptions{MaxConnections: 4, EFConstruction: 16, EFSearch: 12})
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Upsert(Item{ID: "east", Vector: []float32{1, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	if err := index.Upsert(Item{ID: "north", Vector: []float32{0, 1, 0}}); err != nil {
		t.Fatal(err)
	}
	if err := index.Upsert(Item{ID: "east", Vector: []float32{0.9, 0.1, 0}}); err != nil {
		t.Fatal(err)
	}
	results, err := index.Search([]float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultIDs(results); !reflect.DeepEqual(got, []string{"east", "north"}) {
		t.Fatalf("Search() IDs = %v", got)
	}
	if !index.Delete("east") || index.Delete("east") {
		t.Fatal("Delete() did not report existence correctly")
	}
	results, err = index.Search([]float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultIDs(results); !reflect.DeepEqual(got, []string{"north"}) {
		t.Fatalf("Search() after Delete IDs = %v", got)
	}
}

func TestHNSWRecallAgainstExactSearch(t *testing.T) {
	t.Parallel()

	items := randomItems(600, 24, 7)
	exact, err := NewExactIndex(24)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := NewHNSW(24, HNSWOptions{MaxConnections: 16, EFConstruction: 120, EFSearch: 80})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if err := exact.Upsert(item); err != nil {
			t.Fatal(err)
		}
		if err := graph.Add(item); err != nil {
			t.Fatal(err)
		}
	}

	var totalRecall float64
	for queryNumber := 0; queryNumber < 30; queryNumber++ {
		query := items[queryNumber*11].Vector
		exactResults, err := exact.Search(query, 10)
		if err != nil {
			t.Fatal(err)
		}
		approximateResults, err := graph.Search(query, 10)
		if err != nil {
			t.Fatal(err)
		}
		totalRecall += RecallAtK(exactResults, approximateResults, 10)
	}
	averageRecall := totalRecall / 30
	if averageRecall < 0.9 {
		t.Fatalf("average recall@10 = %.3f, want at least 0.900", averageRecall)
	}
}

func TestReciprocalRankFusion(t *testing.T) {
	t.Parallel()

	results := ReciprocalRankFusion([][]string{
		{"lexical-first", "shared", "lexical-last"},
		{"vector-first", "shared", "vector-last"},
	}, 3)
	if got := fusedResultIDs(results); !reflect.DeepEqual(got, []string{"shared", "lexical-first", "vector-first"}) {
		t.Fatalf("ReciprocalRankFusion() IDs = %v", got)
	}
}

func TestEmbedderContract(t *testing.T) {
	t.Parallel()

	var embedder Embedder = embeddingStub{}
	vectors, err := embedder.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || len(vectors[0]) != 2 {
		t.Fatalf("Embed() returned dimensions %v", vectors)
	}
}

type embeddingStub struct{}

func (embeddingStub) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for position := range texts {
		vectors[position] = []float32{float32(position + 1), 1}
	}
	return vectors, nil
}

func randomItems(count, dimensions int, seed int64) []Item {
	random := rand.New(rand.NewSource(seed)) //nolint:gosec // Reproducibility is required for evaluation data.
	items := make([]Item, count)
	for itemNumber := range count {
		values := make([]float32, dimensions)
		for dimension := range dimensions {
			values[dimension] = float32(random.NormFloat64())
		}
		items[itemNumber] = Item{ID: itemID(itemNumber), Vector: values}
	}
	return items
}

func itemID(number int) string {
	const digits = "0123456789"
	encoded := []byte("item-00000")
	for position := len(encoded) - 1; position >= len("item-"); position-- {
		encoded[position] = digits[number%10]
		number /= 10
	}
	return string(encoded)
}

func resultIDs(results []Result) []string {
	ids := make([]string, len(results))
	for position, result := range results {
		ids[position] = result.ID
	}
	return ids
}

func fusedResultIDs(results []FusedResult) []string {
	ids := make([]string, len(results))
	for position, result := range results {
		ids[position] = result.ID
	}
	return ids
}
