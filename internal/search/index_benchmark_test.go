package search

import (
	"fmt"
	"testing"
)

func BenchmarkIndexSearch(b *testing.B) {
	index := NewIndex()
	for documentNumber := 0; documentNumber < 1000; documentNumber++ {
		document := Document{
			ID:    fmt.Sprintf("doc-%04d", documentNumber),
			Title: fmt.Sprintf("Private search document %d", documentNumber),
			Body:  "Distributed nodes keep local documents searchable without centralizing their content.",
		}
		if err := index.Upsert(document); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := index.Search("private local search", MatchAll, 20); err != nil {
			b.Fatal(err)
		}
	}
}
