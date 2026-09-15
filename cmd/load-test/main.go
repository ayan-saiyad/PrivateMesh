// Command load-test measures the running distributed search API under concurrent load.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"
)

type loadConfig struct {
	endpoint     string
	requests     int
	concurrency  int
	timeout      time.Duration
	p95Budget    time.Duration
	maxErrorRate float64
}

type requestResult struct {
	duration time.Duration
	err      error
}

type loadReport struct {
	Endpoint          string  `json:"endpoint"`
	Requests          int     `json:"requests"`
	Concurrency       int     `json:"concurrency"`
	Successful        int     `json:"successful"`
	Failed            int     `json:"failed"`
	ErrorRate         float64 `json:"error_rate"`
	RequestsPerSecond float64 `json:"requests_per_second"`
	LatencyP50MS      float64 `json:"latency_p50_ms"`
	LatencyP95MS      float64 `json:"latency_p95_ms"`
	LatencyP99MS      float64 `json:"latency_p99_ms"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout, http.DefaultTransport); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string, output io.Writer, transport http.RoundTripper) error {
	flags := flag.NewFlagSet("load-test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := loadConfig{}
	flags.StringVar(&config.endpoint, "url", "http://127.0.0.1:18080/api/search", "search endpoint")
	flags.IntVar(&config.requests, "requests", 1000, "total requests")
	flags.IntVar(&config.concurrency, "concurrency", 25, "parallel workers")
	flags.DurationVar(&config.timeout, "timeout", 2*time.Second, "per-request timeout")
	flags.DurationVar(&config.p95Budget, "p95-budget", 500*time.Millisecond, "maximum p95 latency")
	flags.Float64Var(&config.maxErrorRate, "max-error-rate", 0.01, "maximum failed-request ratio")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if err := config.validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"query": "distributed local ownership", "mode": "auto", "limit": 10,
	})
	if err != nil {
		return err
	}
	client := &http.Client{Transport: transport}
	jobs := make(chan struct{})
	results := make(chan requestResult, config.requests)
	var workers sync.WaitGroup
	for range config.concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range jobs {
				results <- executeRequest(client, config, payload)
			}
		}()
	}
	started := time.Now()
	go func() {
		for range config.requests {
			jobs <- struct{}{}
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	report := loadReport{Endpoint: config.endpoint, Requests: config.requests, Concurrency: config.concurrency}
	latencies := make([]time.Duration, 0, config.requests)
	for result := range results {
		latencies = append(latencies, result.duration)
		if result.err != nil {
			report.Failed++
		} else {
			report.Successful++
		}
	}
	elapsed := time.Since(started)
	report.ErrorRate = float64(report.Failed) / float64(report.Requests)
	report.RequestsPerSecond = float64(report.Requests) / elapsed.Seconds()
	report.LatencyP50MS = durationMilliseconds(latencyPercentile(latencies, 0.50))
	report.LatencyP95MS = durationMilliseconds(latencyPercentile(latencies, 0.95))
	report.LatencyP99MS = durationMilliseconds(latencyPercentile(latencies, 0.99))
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return err
	}
	if report.ErrorRate > config.maxErrorRate {
		return fmt.Errorf("error rate %.4f exceeded %.4f", report.ErrorRate, config.maxErrorRate)
	}
	if latencyPercentile(latencies, 0.95) > config.p95Budget {
		return fmt.Errorf("p95 latency %.2fms exceeded %.2fms", report.LatencyP95MS, durationMilliseconds(config.p95Budget))
	}
	return nil
}

func (c loadConfig) validate() error {
	if c.endpoint == "" {
		return errors.New("url is required")
	}
	if c.requests < 1 || c.concurrency < 1 || c.concurrency > c.requests {
		return errors.New("requests and concurrency must be positive, and concurrency cannot exceed requests")
	}
	if c.timeout <= 0 || c.p95Budget <= 0 {
		return errors.New("timeout and p95 budget must be positive")
	}
	if c.maxErrorRate < 0 || c.maxErrorRate > 1 {
		return errors.New("max error rate must be between zero and one")
	}
	return nil
}

func executeRequest(client *http.Client, config loadConfig, payload []byte) requestResult {
	ctx, cancel := context.WithTimeout(context.Background(), config.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.endpoint, bytes.NewReader(payload))
	if err != nil {
		return requestResult{err: err}
	}
	request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	response, err := client.Do(request)
	duration := time.Since(started)
	if err != nil {
		return requestResult{duration: duration, err: err}
	}
	defer func() { _ = response.Body.Close() }()
	_, copyErr := io.Copy(io.Discard, response.Body)
	if copyErr != nil {
		return requestResult{duration: duration, err: copyErr}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return requestResult{duration: duration, err: fmt.Errorf("unexpected status %d", response.StatusCode)}
	}
	return requestResult{duration: duration}
}

func latencyPercentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	position := int(quantile*float64(len(sorted)-1) + 0.5)
	return sorted[max(0, min(position, len(sorted)-1))]
}

func durationMilliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
