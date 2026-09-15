package planner

import (
	"errors"
	"time"
)

// Outcome is measured retrieval quality and latency for one strategy.
type Outcome struct {
	Quality float64
	Latency time.Duration
}

// EvaluationCase contains a request and measured outcomes for each strategy.
type EvaluationCase struct {
	Request  Request
	Outcomes map[Strategy]Outcome
}

// PolicyMetrics summarizes a planner or fixed strategy across an evaluation corpus.
type PolicyMetrics struct {
	MeanQuality      float64
	MeanLatency      time.Duration
	DeadlineMissRate float64
}

// Comparison reports adaptive and fixed-strategy performance.
type Comparison struct {
	Adaptive map[Strategy]int
	Policies map[string]PolicyMetrics
}

// Compare evaluates adaptive selection against fixed retrieval policies.
func Compare(planner *Planner, cases []EvaluationCase) (Comparison, error) {
	if planner == nil || len(cases) == 0 {
		return Comparison{}, errors.New("planner and evaluation cases are required")
	}
	selections := make(map[Strategy]int)
	accumulators := map[string]*policyAccumulator{
		"adaptive": {},
		"lexical":  {},
		"vector":   {},
		"hybrid":   {},
	}
	for _, evaluation := range cases {
		plan, err := planner.Select(evaluation.Request)
		if err != nil {
			return Comparison{}, err
		}
		selections[plan.Strategy]++
		adaptive, ok := evaluation.Outcomes[plan.Strategy]
		if !ok {
			return Comparison{}, errors.New("evaluation case is missing selected strategy outcome")
		}
		accumulators["adaptive"].add(adaptive, evaluation.Request.Deadline)
		for _, strategy := range strategies {
			outcome, ok := evaluation.Outcomes[strategy]
			if !ok {
				return Comparison{}, errors.New("evaluation case is missing fixed strategy outcome")
			}
			accumulators[string(strategy)].add(outcome, evaluation.Request.Deadline)
		}
	}
	policies := make(map[string]PolicyMetrics, len(accumulators))
	for name, accumulator := range accumulators {
		policies[name] = accumulator.metrics(len(cases))
	}
	return Comparison{Adaptive: selections, Policies: policies}, nil
}

type policyAccumulator struct {
	quality float64
	latency time.Duration
	misses  int
}

func (a *policyAccumulator) add(outcome Outcome, deadline time.Duration) {
	a.quality += outcome.Quality
	a.latency += outcome.Latency
	if outcome.Latency > deadline {
		a.misses++
	}
}

func (a *policyAccumulator) metrics(count int) PolicyMetrics {
	return PolicyMetrics{
		MeanQuality:      a.quality / float64(count),
		MeanLatency:      a.latency / time.Duration(count),
		DeadlineMissRate: float64(a.misses) / float64(count),
	}
}
