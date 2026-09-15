package vector

import "testing"

func BenchmarkVectorSearch(b *testing.B) {
	const dimensions = 64
	items := randomItems(5000, dimensions, 21)
	exact, err := NewExactIndex(dimensions)
	if err != nil {
		b.Fatal(err)
	}
	graph, err := NewHNSW(dimensions, HNSWOptions{
		MaxConnections: 16,
		EFConstruction: 120,
		EFSearch:       80,
	})
	if err != nil {
		b.Fatal(err)
	}
	for _, item := range items {
		if err := exact.Upsert(item); err != nil {
			b.Fatal(err)
		}
		if err := graph.Add(item); err != nil {
			b.Fatal(err)
		}
	}
	query := items[2718].Vector

	b.Run("exact", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := exact.Search(query, 10); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("hnsw", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := graph.Search(query, 10); err != nil {
				b.Fatal(err)
			}
		}
	})
}
