package vector

import "sort"

const reciprocalRankConstant = 60

// FusedResult is an item ranked across multiple retrieval strategies.
type FusedResult struct {
	ID    string
	Score float64
}

// ReciprocalRankFusion combines ordered result IDs without comparing incompatible scores.
func ReciprocalRankFusion(rankings [][]string, limit int) []FusedResult {
	scores := make(map[string]float64)
	for _, ranking := range rankings {
		seen := make(map[string]struct{}, len(ranking))
		for position, id := range ranking {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			scores[id] += 1 / float64(reciprocalRankConstant+position+1)
		}
	}
	results := make([]FusedResult, 0, len(scores))
	for id, score := range scores {
		results = append(results, FusedResult{ID: id, Score: score})
	}
	sort.Slice(results, func(left, right int) bool {
		if results[left].Score == results[right].Score {
			return results[left].ID < results[right].ID
		}
		return results[left].Score > results[right].Score
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

// RecallAtK measures how many exact neighbors appear in an approximate result set.
func RecallAtK(exact, approximate []Result, limit int) float64 {
	if limit <= 0 {
		return 0
	}
	exactLimit := min(limit, len(exact))
	if exactLimit == 0 {
		return 1
	}
	relevant := make(map[string]struct{}, exactLimit)
	for _, result := range exact[:exactLimit] {
		relevant[result.ID] = struct{}{}
	}
	approximateLimit := min(limit, len(approximate))
	matches := 0
	for _, result := range approximate[:approximateLimit] {
		if _, ok := relevant[result.ID]; ok {
			matches++
		}
	}
	return float64(matches) / float64(exactLimit)
}
