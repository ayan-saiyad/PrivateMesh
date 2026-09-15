# Build status

The planned implementation is complete for the v1.0.0 project release.

## Search and storage

- [x] Unicode normalization, inverted indexing, BM25, and deterministic top-k retrieval
- [x] Exact vector search, HNSW, reciprocal-rank fusion, and recall evaluation
- [x] Immutable checksummed segments with atomic publication and merging
- [x] Committed write-ahead log with replay, truncation, corruption detection, and recovery tests

## Distribution and security

- [x] Lease-backed node registry and collection catalog
- [x] Deadline-aware fan-out, cancellation, streaming updates, and global result merging
- [x] Explicit partial coverage when nodes are unavailable
- [x] Primary election, replicated log offsets, WAL catch-up, and snapshot installation
- [x] OIDC verification and short-lived signed principal propagation
- [x] Node-local collection and document policy enforcement
- [x] Content-free authorization audit records
- [x] Health- and deadline-aware lexical, vector, and hybrid planning

## Delivery and operations

- [x] Search API and browser application connected to a live two-node topology
- [x] OpenTelemetry traces and Prometheus metrics without protected payload attributes
- [x] Provisioned Grafana and Jaeger views
- [x] Concurrent load, node-loss, and durable-recovery checks
- [x] Reproducible one-million-document benchmark
- [x] Compose and Kubernetes deployments with an operator runbook
- [x] Automated validation and version-tagged container publishing
