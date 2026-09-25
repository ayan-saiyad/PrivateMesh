// Package vector provides local vector indexing and hybrid result fusion.
package vector

import (
	"context"
	"errors"
	"math"
)

var (
	// ErrVectorIDRequired indicates that an item has no usable identifier.
	ErrVectorIDRequired = errors.New("vector ID is required")
	// ErrDimensionMismatch indicates that a vector has an unexpected number of values.
	ErrDimensionMismatch = errors.New("vector dimension mismatch")
	// ErrInvalidVector indicates that a vector is empty, non-finite, or has zero magnitude.
	ErrInvalidVector = errors.New("invalid vector")
	// ErrDuplicateID indicates that an item with the same ID already exists.
	ErrDuplicateID = errors.New("duplicate vector ID")
)

// Embedder converts text into vectors without prescribing a model provider.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Item associates a document ID with an embedding vector.
type Item struct {
	ID     string
	Vector []float32
}

// Result is a vector match and its cosine similarity.
type Result struct {
	ID    string
	Score float32
}

func normalize(values []float32, dimensions int) ([]float32, error) {
	if len(values) == 0 {
		return nil, ErrInvalidVector
	}
	if len(values) != dimensions {
		return nil, ErrDimensionMismatch
	}
	var magnitudeSquared float64
	for _, value := range values {
		converted := float64(value)
		if math.IsNaN(converted) || math.IsInf(converted, 0) {
			return nil, ErrInvalidVector
		}
		magnitudeSquared += converted * converted
	}
	if magnitudeSquared == 0 {
		return nil, ErrInvalidVector
	}
	inverseMagnitude := float32(1 / math.Sqrt(magnitudeSquared))
	normalized := make([]float32, len(values))
	for position, value := range values {
		normalized[position] = value * inverseMagnitude
	}
	return normalized, nil
}

func cosine(left, right []float32) float32 {
	var score float32
	for position := range left {
		score += left[position] * right[position]
	}
	return score
}
