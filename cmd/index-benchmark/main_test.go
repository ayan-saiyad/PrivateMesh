package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestRunProducesReproducibleCorpusReport(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := run([]string{"-documents", "200", "-queries", "10", "-seed", "7"}, &output); err != nil {
		t.Fatal(err)
	}
	var report benchmarkReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Documents != 200 || report.Queries != 10 || report.Seed != 7 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.ResultChecksum == 0 || report.DocumentsPerSec <= 0 || report.QueriesPerSec <= 0 {
		t.Fatalf("incomplete report: %+v", report)
	}
}

func TestPercentileUsesNearestRank(t *testing.T) {
	t.Parallel()
	values := []time.Duration{4 * time.Millisecond, time.Millisecond, 3 * time.Millisecond, 2 * time.Millisecond}
	if got := percentile(values, 0.95); got != 4*time.Millisecond {
		t.Fatalf("percentile() = %s", got)
	}
}
