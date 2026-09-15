// Package embedding provides local embedding implementations.
package embedding

import (
	"context"
	"errors"
	"hash/fnv"
	"strings"
	"unicode"
)

// Hash converts normalized tokens and character trigrams into deterministic vectors.
type Hash struct {
	dimensions int
}

// NewHash creates a deterministic local embedder.
func NewHash(dimensions int) (*Hash, error) {
	if dimensions < 16 {
		return nil, errors.New("embedding dimensions must be at least 16")
	}
	return &Hash{dimensions: dimensions}, nil
}

// Embed converts text into fixed-size feature vectors.
func (h *Hash) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for index, text := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		vector := make([]float32, h.dimensions)
		vector[0] = 1
		for _, token := range tokens(text) {
			h.add(vector, "word:"+token, 2)
			runes := []rune("^" + token + "$")
			for start := 0; start+3 <= len(runes); start++ {
				h.add(vector, "gram:"+string(runes[start:start+3]), 0.5)
			}
		}
		vectors[index] = vector
	}
	return vectors, nil
}

func (h *Hash) add(vector []float32, feature string, weight float32) {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(feature))
	value := hash.Sum64()
	position := value % uint64(len(vector))
	if value&(1<<63) == 0 {
		vector[position] += weight
	} else {
		vector[position] -= weight
	}
}

func tokens(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(value rune) bool {
		return !unicode.IsLetter(value) && !unicode.IsNumber(value)
	})
}
