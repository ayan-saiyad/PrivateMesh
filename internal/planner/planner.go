// Package planner selects a retrieval strategy from query, deadline, and node health signals.
package planner

import (
	"errors"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const observationWindow = 256

// Strategy identifies a retrieval implementation.
type Strategy string

const (
	// Lexical uses the term index.
	Lexical Strategy = "lexical"
	// Vector uses the nearest-neighbor index.
	Vector Strategy = "vector"
	// Hybrid fuses lexical and vector rankings.
	Hybrid Strategy = "hybrid"
)

var strategies = [...]Strategy{Lexical, Vector, Hybrid}

// Baseline summarizes recent performance for a strategy.
type Baseline struct {
	Quality   float64
	P95       time.Duration
	Samples   int
	Available bool
}

// Observation is one completed strategy execution.
type Observation struct {
	Latency time.Duration
	Quality float64
}

// Tracker maintains bounded per-strategy quality and latency baselines.
type Tracker struct {
	mu           sync.RWMutex
	observations map[Strategy][]Observation
	priors       map[Strategy]Baseline
}

// NewTracker creates a baseline tracker with configured priors.
func NewTracker(priors map[Strategy]Baseline) (*Tracker, error) {
	if len(priors) == 0 {
		return nil, errors.New("strategy priors are required")
	}
	cloned := make(map[Strategy]Baseline, len(priors))
	for strategy, baseline := range priors {
		if !validStrategy(strategy) || baseline.Quality < 0 || baseline.Quality > 1 || baseline.P95 <= 0 {
			return nil, errors.New("invalid strategy baseline")
		}
		baseline.Samples = 0
		baseline.Available = true
		cloned[strategy] = baseline
	}
	return &Tracker{observations: make(map[Strategy][]Observation), priors: cloned}, nil
}

// Record adds a completed strategy observation.
func (t *Tracker) Record(strategy Strategy, observation Observation) error {
	if !validStrategy(strategy) || observation.Latency <= 0 || observation.Quality < 0 || observation.Quality > 1 {
		return errors.New("invalid strategy observation")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	current := t.observations[strategy]
	values := make([]Observation, len(current)+1)
	copy(values, current)
	values[len(current)] = observation
	if len(values) > observationWindow {
		values = append([]Observation(nil), values[len(values)-observationWindow:]...)
	}
	t.observations[strategy] = values
	return nil
}

// Snapshot returns current baselines for every configured strategy.
func (t *Tracker) Snapshot() map[Strategy]Baseline {
	t.mu.RLock()
	defer t.mu.RUnlock()
	baselines := make(map[Strategy]Baseline, len(t.priors))
	for strategy, prior := range t.priors {
		values := t.observations[strategy]
		if len(values) == 0 {
			baselines[strategy] = prior
			continue
		}
		latencies := make([]time.Duration, len(values))
		quality := 0.0
		for index, value := range values {
			latencies[index] = value.Latency
			quality += value.Quality
		}
		sort.Slice(latencies, func(left, right int) bool { return latencies[left] < latencies[right] })
		p95Index := int(math.Ceil(float64(len(latencies))*0.95)) - 1
		baselines[strategy] = Baseline{
			Quality:   quality / float64(len(values)),
			P95:       latencies[p95Index],
			Samples:   len(values),
			Available: true,
		}
	}
	return baselines
}

// Features are query properties used during strategy selection.
type Features struct {
	TokenCount      int
	RuneCount       int
	HasQuotedPhrase bool
	LooksLikeID     bool
	LooksNatural    bool
}

// ExtractFeatures computes bounded, content-independent planner inputs.
func ExtractFeatures(query string) Features {
	query = strings.TrimSpace(query)
	tokens := strings.Fields(query)
	features := Features{
		TokenCount:      len(tokens),
		RuneCount:       len([]rune(query)),
		HasQuotedPhrase: hasQuotedPhrase(query),
	}
	if len(tokens) == 1 {
		features.LooksLikeID = looksLikeID(tokens[0])
	}
	lower := strings.ToLower(query)
	features.LooksNatural = len(tokens) >= 6 || strings.HasSuffix(query, "?") ||
		strings.HasPrefix(lower, "how ") || strings.HasPrefix(lower, "why ") ||
		strings.HasPrefix(lower, "what ") || strings.HasPrefix(lower, "which ")
	return features
}

// Health summarizes retrieval capacity for the selected shards.
type Health struct {
	TotalNodes       int
	AvailableNodes   int
	VectorReadyNodes int
}

// Request contains inputs for one planning decision.
type Request struct {
	Query    string
	Deadline time.Duration
	Health   Health
}

// Plan describes a selected strategy and its expected performance.
type Plan struct {
	Strategy         Strategy
	Features         Features
	EstimatedQuality float64
	EstimatedLatency time.Duration
	Reason           string
}

// Planner selects the highest-quality strategy expected to meet the request deadline.
type Planner struct {
	tracker *Tracker
	margin  float64
}

// New creates an adaptive query planner.
func New(tracker *Tracker, deadlineMargin float64) (*Planner, error) {
	if tracker == nil {
		return nil, errors.New("baseline tracker is required")
	}
	if deadlineMargin <= 0 || deadlineMargin > 1 {
		return nil, errors.New("deadline margin must be in (0, 1]")
	}
	return &Planner{tracker: tracker, margin: deadlineMargin}, nil
}

// Select chooses a strategy for one query.
func (p *Planner) Select(request Request) (Plan, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return Plan{}, errors.New("query is required")
	}
	if request.Deadline <= 0 {
		return Plan{}, errors.New("query deadline must be positive")
	}
	if request.Health.TotalNodes <= 0 || request.Health.AvailableNodes < 0 ||
		request.Health.AvailableNodes > request.Health.TotalNodes || request.Health.VectorReadyNodes < 0 ||
		request.Health.VectorReadyNodes > request.Health.AvailableNodes {
		return Plan{}, errors.New("invalid node health")
	}

	features := ExtractFeatures(request.Query)
	baselines := p.tracker.Snapshot()
	budget := time.Duration(float64(request.Deadline) * p.margin)
	candidates := make([]Plan, 0, len(strategies))
	for _, strategy := range strategies {
		baseline, ok := baselines[strategy]
		if !ok || !baseline.Available || !healthy(strategy, request.Health) {
			continue
		}
		latency := healthAdjustedLatency(strategy, baseline.P95, request.Health)
		if latency > budget {
			continue
		}
		candidates = append(candidates, Plan{
			Strategy:         strategy,
			Features:         features,
			EstimatedQuality: adjustedQuality(strategy, baseline.Quality, features, request.Health),
			EstimatedLatency: latency,
			Reason:           "highest expected quality within deadline",
		})
	}
	if len(candidates) == 0 {
		baseline, ok := baselines[Lexical]
		if !ok {
			return Plan{}, errors.New("no retrieval strategy is available")
		}
		return Plan{
			Strategy: Lexical, Features: features, EstimatedQuality: adjustedQuality(Lexical, baseline.Quality, features, request.Health),
			EstimatedLatency: baseline.P95, Reason: "fastest fallback exceeds requested margin",
		}, nil
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].EstimatedQuality == candidates[right].EstimatedQuality {
			if candidates[left].EstimatedLatency == candidates[right].EstimatedLatency {
				return strategyOrder(candidates[left].Strategy) < strategyOrder(candidates[right].Strategy)
			}
			return candidates[left].EstimatedLatency < candidates[right].EstimatedLatency
		}
		return candidates[left].EstimatedQuality > candidates[right].EstimatedQuality
	})
	return candidates[0], nil
}

func adjustedQuality(strategy Strategy, baseline float64, features Features, health Health) float64 {
	quality := baseline
	if features.HasQuotedPhrase || features.LooksLikeID {
		switch strategy {
		case Lexical:
			quality += 0.08
		case Vector:
			quality -= 0.08
		case Hybrid:
			quality -= 0.06
		}
	}
	if features.LooksNatural {
		switch strategy {
		case Lexical:
			quality -= 0.02
		case Vector:
			quality += 0.05
		case Hybrid:
			quality += 0.04
		}
	}
	if strategy == Vector || strategy == Hybrid {
		quality *= float64(health.VectorReadyNodes) / float64(health.AvailableNodes)
	}
	return math.Max(0, math.Min(1, quality))
}

func healthAdjustedLatency(strategy Strategy, latency time.Duration, health Health) time.Duration {
	if strategy == Lexical {
		return latency
	}
	ratio := float64(health.VectorReadyNodes) / float64(health.AvailableNodes)
	return time.Duration(float64(latency) / ratio)
}

func healthy(strategy Strategy, health Health) bool {
	if health.AvailableNodes == 0 {
		return false
	}
	if strategy == Lexical {
		return true
	}
	return health.VectorReadyNodes > 0
}

func validStrategy(strategy Strategy) bool {
	return strategy == Lexical || strategy == Vector || strategy == Hybrid
}

func strategyOrder(strategy Strategy) int {
	for index, candidate := range strategies {
		if strategy == candidate {
			return index
		}
	}
	return len(strategies)
}

func hasQuotedPhrase(query string) bool {
	open := false
	content := false
	for _, value := range query {
		if value == '"' {
			if open && content {
				return true
			}
			open = !open
			content = false
			continue
		}
		if open && !unicode.IsSpace(value) {
			content = true
		}
	}
	return false
}

func looksLikeID(value string) bool {
	if len(value) < 4 || strings.ContainsAny(value, " ") {
		return false
	}
	separator := false
	digit := false
	for _, current := range value {
		if current == '-' || current == '_' || current == ':' || current == '/' {
			separator = true
		}
		if unicode.IsDigit(current) {
			digit = true
		}
	}
	return separator && digit
}
