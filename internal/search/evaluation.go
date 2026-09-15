package search

import (
	"math"
	"sort"
)

// NDCG measures ranked retrieval quality against graded relevance judgments.
func NDCG(results []Result, relevance map[string]uint8, limit int) float64 {
	if limit <= 0 {
		limit = len(results)
	}

	actualLimit := min(limit, len(results))
	actual := make([]uint8, 0, actualLimit)
	for _, result := range results[:actualLimit] {
		actual = append(actual, relevance[result.Document.ID])
	}
	actualGain := discountedCumulativeGain(actual)

	ideal := make([]uint8, 0, len(relevance))
	for _, grade := range relevance {
		ideal = append(ideal, grade)
	}
	sort.Slice(ideal, func(left, right int) bool {
		return ideal[left] > ideal[right]
	})
	if idealLimit := min(limit, len(ideal)); len(ideal) > idealLimit {
		ideal = ideal[:idealLimit]
	}
	idealGain := discountedCumulativeGain(ideal)
	if idealGain == 0 {
		return 0
	}
	return actualGain / idealGain
}

func discountedCumulativeGain(grades []uint8) float64 {
	var gain float64
	for position, grade := range grades {
		gain += (math.Pow(2, float64(grade)) - 1) / math.Log2(float64(position)+2)
	}
	return gain
}
