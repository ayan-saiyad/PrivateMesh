// Command index-benchmark measures deterministic local indexing and search performance.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"slices"
	"time"

	"github.com/ayansaiyad/privatemesh/internal/search"
)

type benchmarkConfig struct {
	documents int
	queries   int
	seed      int
}

type benchmarkReport struct {
	Documents        int     `json:"documents"`
	Queries          int     `json:"queries"`
	Seed             int     `json:"seed"`
	IndexDurationMS  int64   `json:"index_duration_ms"`
	DocumentsPerSec  float64 `json:"documents_per_second"`
	QueriesPerSec    float64 `json:"queries_per_second"`
	QueryP50MS       float64 `json:"query_p50_ms"`
	QueryP95MS       float64 `json:"query_p95_ms"`
	QueryP99MS       float64 `json:"query_p99_ms"`
	HeapAllocatedMiB float64 `json:"heap_allocated_mib"`
	ResultChecksum   uint64  `json:"result_checksum"`
	GoVersion        string  `json:"go_version"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("index-benchmark", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := benchmarkConfig{}
	flags.IntVar(&config.documents, "documents", 1_000_000, "number of documents to index")
	flags.IntVar(&config.queries, "queries", 500, "number of searches to execute")
	flags.IntVar(&config.seed, "seed", 20260915, "deterministic corpus seed")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if config.documents < 1 {
		return errors.New("documents must be positive")
	}
	if config.queries < 1 {
		return errors.New("queries must be positive")
	}
	if config.documents > 10_000_000 || config.queries > 10_000_000 {
		return errors.New("documents and queries cannot exceed ten million")
	}
	if config.seed < 0 || config.seed > 1_000_000_000 {
		return errors.New("seed must be between zero and one billion")
	}

	index := search.NewIndex()
	indexStarted := time.Now()
	for number := 0; number < config.documents; number++ {
		if err := index.Upsert(generatedDocument(number, config.seed)); err != nil {
			return fmt.Errorf("index document %d: %w", number, err)
		}
	}
	indexDuration := time.Since(indexStarted)
	if index.Len() != config.documents {
		return fmt.Errorf("indexed %d documents, expected %d", index.Len(), config.documents)
	}

	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	latencies := make([]time.Duration, 0, config.queries)
	var checksum uint64
	queryStarted := time.Now()
	for number := 0; number < config.queries; number++ {
		documentNumber := (number*7919 + config.seed) % config.documents
		query := generatedQuery(documentNumber, config.seed)
		started := time.Now()
		results, err := index.Search(query, search.MatchAll, 20)
		latencies = append(latencies, time.Since(started))
		if err != nil {
			return fmt.Errorf("search query %d: %w", number, err)
		}
		checksum += uint64(len(results))
	}
	queryDuration := time.Since(queryStarted)

	report := benchmarkReport{
		Documents:        config.documents,
		Queries:          config.queries,
		Seed:             config.seed,
		IndexDurationMS:  indexDuration.Milliseconds(),
		DocumentsPerSec:  float64(config.documents) / indexDuration.Seconds(),
		QueriesPerSec:    float64(config.queries) / queryDuration.Seconds(),
		QueryP50MS:       milliseconds(percentile(latencies, 0.50)),
		QueryP95MS:       milliseconds(percentile(latencies, 0.95)),
		QueryP99MS:       milliseconds(percentile(latencies, 0.99)),
		HeapAllocatedMiB: float64(memory.Alloc) / (1024 * 1024),
		ResultChecksum:   checksum,
		GoVersion:        runtime.Version(),
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func generatedDocument(number, seed int) search.Document {
	topic, region, owner := generatedTerms(number, seed)
	return search.Document{
		ID:    fmt.Sprintf("document-%09d", number),
		Title: fmt.Sprintf("topic%d owner%d", topic, owner),
		Body:  fmt.Sprintf("region%d record%d", region, number),
	}
}

func generatedQuery(number, seed int) string {
	topic, region, _ := generatedTerms(number, seed)
	return fmt.Sprintf("topic%d region%d", topic, region)
}

func generatedTerms(number, seed int) (int, int, int) {
	return (number*104729 + seed) % 4096,
		(number*65537 + seed*3) % 256,
		(number*32771 + seed*7) % 16384
}

func percentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	position := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	position = max(0, min(position, len(sorted)-1))
	return sorted[position]
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
