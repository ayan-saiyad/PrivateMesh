# Roadmap

Each phase must satisfy its acceptance criteria before work starts on the next one.

## Phase 0: Foundation

- Runnable coordinator and search-node processes
- Health, readiness, and build-information endpoints
- Versioned protobuf contracts
- Containerized development and quality tooling
- Minimal web application shell
- CI for formatting, linting, tests, builds, and protocol validation
- Initial architecture and threat-model documents

## Phase 1: In-memory lexical retrieval

- Deterministic tokenizer and normalization pipeline
- In-memory inverted index
- Boolean AND/OR query execution
- Document ingestion and deletion
- Unit tests for Unicode, duplicate terms, and empty documents

## Phase 2: BM25 and relevance evaluation

- BM25 scoring implemented in the search core
- Field-aware title and body scoring
- Deterministic top-k behavior
- Small labeled relevance corpus and NDCG evaluation
- Baseline latency and allocation benchmarks

## Phase 3: Persistent index segments

- Immutable segment file format
- Checksums and versioned headers
- Delta-encoded postings
- Atomic segment publication
- Segment merge policy and crash-safe recovery tests

## Phase 4: Write-ahead log

- Append, commit, replay, and truncation semantics
- Recovery after forced termination
- Idempotent document operations
- Corruption detection and bounded recovery time

## Phase 5: Vector and hybrid retrieval

- Local embedding-service contract
- Exact vector-search baseline
- HNSW construction and querying
- Reciprocal-rank fusion
- Recall and latency comparison against exact search

## Phase 6: Distributed query execution

- Node registration and lease-backed heartbeats
- Shard catalog and query fan-out
- Deadline propagation and cancellation
- Streaming partial results
- Global top-k merging

## Phase 7: Replication and recovery

- Primary-replica write protocol
- Committed log offsets
- Lease-based primary election
- Replica catch-up from WAL and snapshots
- Fault-injection tests for partitions and process loss

## Phase 8: Identity and authorization

- OIDC login and signed principal propagation
- Collection and document policy model
- Local authorization enforcement
- Audit events that exclude document and query content
- Negative authorization tests

## Phase 9: Adaptive query planning

- Per-strategy quality and latency baselines
- Planner feature extraction
- Deadline- and health-aware strategy selection
- Comparison with always-lexical, always-vector, and always-hybrid policies

## Phase 10: Operations and release

- OpenTelemetry traces and metrics
- Prometheus and Grafana dashboards
- Load, chaos, and recovery test suites
- Reproducible million-document benchmark
- Kubernetes deployment and operator runbook
- Public demonstration and versioned release

