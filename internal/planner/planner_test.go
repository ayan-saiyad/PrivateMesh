package planner

import (
	"testing"
	"time"
)

func TestExtractFeatures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		query string
		check func(Features) bool
	}{
		{query: `"exact service name"`, check: func(features Features) bool { return features.HasQuotedPhrase }},
		{query: "ticket-1042", check: func(features Features) bool { return features.LooksLikeID }},
		{query: "How do replicas recover when a primary node fails?", check: func(features Features) bool { return features.LooksNatural }},
	}
	for _, test := range tests {
		features := ExtractFeatures(test.query)
		if !test.check(features) {
			t.Fatalf("ExtractFeatures(%q) = %+v", test.query, features)
		}
	}
}

func TestTrackerCalculatesBoundedP95(t *testing.T) {
	t.Parallel()

	tracker := testTracker(t)
	for index := 1; index <= 300; index++ {
		if err := tracker.Record(Lexical, Observation{Latency: time.Duration(index) * time.Millisecond, Quality: 0.8}); err != nil {
			t.Fatal(err)
		}
	}
	baseline := tracker.Snapshot()[Lexical]
	if baseline.Samples != observationWindow {
		t.Fatalf("samples = %d, want %d", baseline.Samples, observationWindow)
	}
	if baseline.P95 != 288*time.Millisecond {
		t.Fatalf("P95 = %s, want 288ms", baseline.P95)
	}
}

func TestPlannerUsesQueryAndDeadlineSignals(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	tests := []struct {
		name     string
		request  Request
		strategy Strategy
	}{
		{
			name: "identifier favors lexical",
			request: Request{Query: "incident-1042", Deadline: 100 * time.Millisecond,
				Health: Health{TotalNodes: 3, AvailableNodes: 3, VectorReadyNodes: 3}},
			strategy: Lexical,
		},
		{
			name: "natural language uses hybrid",
			request: Request{Query: "How do I recover a replica after an unexpected process failure?", Deadline: 100 * time.Millisecond,
				Health: Health{TotalNodes: 3, AvailableNodes: 3, VectorReadyNodes: 3}},
			strategy: Hybrid,
		},
		{
			name: "tight deadline uses lexical",
			request: Request{Query: "distributed indexing recovery behavior", Deadline: 18 * time.Millisecond,
				Health: Health{TotalNodes: 3, AvailableNodes: 3, VectorReadyNodes: 3}},
			strategy: Lexical,
		},
		{
			name: "vector outage uses lexical",
			request: Request{Query: "How do ownership boundaries affect private federated search?", Deadline: 100 * time.Millisecond,
				Health: Health{TotalNodes: 3, AvailableNodes: 2, VectorReadyNodes: 0}},
			strategy: Lexical,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, err := planner.Select(test.request)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Strategy != test.strategy {
				t.Fatalf("Select() strategy = %s, want %s; plan=%+v", plan.Strategy, test.strategy, plan)
			}
		})
	}
}

func TestCompareReportsAdaptiveAndFixedPolicies(t *testing.T) {
	t.Parallel()

	planner := testPlanner(t)
	cases := []EvaluationCase{
		{
			Request: Request{Query: "record-1001", Deadline: 20 * time.Millisecond, Health: Health{TotalNodes: 2, AvailableNodes: 2, VectorReadyNodes: 2}},
			Outcomes: map[Strategy]Outcome{
				Lexical: {Quality: 1.0, Latency: 8 * time.Millisecond},
				Vector:  {Quality: 0.5, Latency: 24 * time.Millisecond},
				Hybrid:  {Quality: 0.9, Latency: 45 * time.Millisecond},
			},
		},
		{
			Request: Request{Query: "How should a replica recover after it misses committed writes?", Deadline: 100 * time.Millisecond, Health: Health{TotalNodes: 2, AvailableNodes: 2, VectorReadyNodes: 2}},
			Outcomes: map[Strategy]Outcome{
				Lexical: {Quality: 0.6, Latency: 8 * time.Millisecond},
				Vector:  {Quality: 0.85, Latency: 24 * time.Millisecond},
				Hybrid:  {Quality: 0.95, Latency: 45 * time.Millisecond},
			},
		},
	}
	comparison, err := Compare(planner, cases)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Adaptive[Lexical] != 1 || comparison.Adaptive[Hybrid] != 1 {
		t.Fatalf("adaptive selections = %v", comparison.Adaptive)
	}
	adaptive := comparison.Policies["adaptive"]
	if adaptive.DeadlineMissRate != 0 || adaptive.MeanQuality <= comparison.Policies["lexical"].MeanQuality {
		t.Fatalf("adaptive metrics = %+v, lexical = %+v", adaptive, comparison.Policies["lexical"])
	}
	if comparison.Policies["hybrid"].DeadlineMissRate == 0 {
		t.Fatalf("hybrid deadline miss rate = %f, want non-zero", comparison.Policies["hybrid"].DeadlineMissRate)
	}
}

func BenchmarkPlannerSelect(b *testing.B) {
	planner := testPlanner(b)
	request := Request{
		Query: "How do search nodes recover committed data after failure?", Deadline: 75 * time.Millisecond,
		Health: Health{TotalNodes: 8, AvailableNodes: 7, VectorReadyNodes: 6},
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := planner.Select(request); err != nil {
			b.Fatal(err)
		}
	}
}

func testPlanner(t testing.TB) *Planner {
	t.Helper()
	planner, err := New(testTracker(t), 0.9)
	if err != nil {
		t.Fatal(err)
	}
	return planner
}

func testTracker(t testing.TB) *Tracker {
	t.Helper()
	tracker, err := NewTracker(map[Strategy]Baseline{
		Lexical: {Quality: 0.78, P95: 10 * time.Millisecond},
		Vector:  {Quality: 0.82, P95: 25 * time.Millisecond},
		Hybrid:  {Quality: 0.9, P95: 50 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	return tracker
}
