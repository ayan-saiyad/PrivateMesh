package embedding

import (
	"context"
	"reflect"
	"testing"
)

func TestHashEmbeddingsAreDeterministicAndMeaningful(t *testing.T) {
	t.Parallel()

	embedder, err := NewHash(128)
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := embedder.Embed(context.Background(), []string{
		"replica recovery after failure",
		"replica recovery after failure",
		"design system typography",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vectors[0], vectors[1]) {
		t.Fatal("equal text produced different embeddings")
	}
	if reflect.DeepEqual(vectors[0], vectors[2]) {
		t.Fatal("different text produced equal embeddings")
	}
}

func TestHashEmbeddingHonorsCancellation(t *testing.T) {
	t.Parallel()

	embedder, err := NewHash(64)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := embedder.Embed(ctx, []string{"text"}); err == nil {
		t.Fatal("Embed() error = nil, want cancellation")
	}
}
