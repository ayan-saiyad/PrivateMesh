package search

import (
	"math"
	"sort"
)

const (
	bm25K1      = 1.2
	titleB      = 0.75
	bodyB       = 0.75
	titleWeight = 2.5
	bodyWeight  = 1.0
)

// Result contains a matching document and its lexical relevance score.
type Result struct {
	Document Document
	Score    float64
}

// Search returns matching documents ordered by relevance. A zero limit returns every match.
func (i *Index) Search(query string, mode MatchMode, limit int) ([]Result, error) {
	if mode != MatchAll && mode != MatchAny {
		return nil, ErrInvalidMatchMode
	}
	if limit < 0 {
		return nil, ErrInvalidLimit
	}

	terms := uniqueTerms(tokenize(query))
	if len(terms) == 0 {
		return []Result{}, nil
	}

	i.mu.RLock()
	defer i.mu.RUnlock()

	documentCount := len(i.documents)
	if documentCount == 0 {
		return []Result{}, nil
	}

	averageTitleLength := float64(i.totalTitleTerms) / float64(documentCount)
	averageBodyLength := float64(i.totalBodyTerms) / float64(documentCount)
	matches := i.matchingDocuments(terms, mode)
	results := make([]Result, 0, len(matches))
	for id := range matches {
		results = append(results, Result{
			Document: i.documents[id],
			Score: i.score(
				id,
				terms,
				float64(documentCount),
				averageTitleLength,
				averageBodyLength,
			),
		})
	}

	sort.Slice(results, func(left, right int) bool {
		if results[left].Score == results[right].Score {
			return results[left].Document.ID < results[right].Document.ID
		}
		return results[left].Score > results[right].Score
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (i *Index) score(
	id string,
	terms []string,
	documentCount float64,
	averageTitleLength float64,
	averageBodyLength float64,
) float64 {
	lengths := i.lengths[id]
	var score float64
	for _, term := range terms {
		posting := i.postings[term][id]
		documentFrequency := float64(len(i.postings[term]))
		inverseDocumentFrequency := math.Log(
			1 + (documentCount-documentFrequency+0.5)/(documentFrequency+0.5),
		)
		titleScore := normalizedTermFrequency(
			posting.titleFrequency,
			lengths.title,
			averageTitleLength,
			titleB,
		)
		bodyScore := normalizedTermFrequency(
			posting.bodyFrequency,
			lengths.body,
			averageBodyLength,
			bodyB,
		)
		score += inverseDocumentFrequency * (titleWeight*titleScore + bodyWeight*bodyScore)
	}
	return score
}

func normalizedTermFrequency(
	frequency uint64,
	fieldLength uint64,
	averageFieldLength float64,
	b float64,
) float64 {
	if frequency == 0 {
		return 0
	}
	if averageFieldLength == 0 {
		averageFieldLength = 1
	}

	tf := float64(frequency)
	lengthRatio := float64(fieldLength) / averageFieldLength
	return tf * (bm25K1 + 1) / (tf + bm25K1*(1-b+b*lengthRatio))
}
