# 0001: Use Go for backend services

- Status: accepted
- Date: 2026-09-11

## Context

The system needs concurrent request fan-out, explicit cancellation, small deployable services, and
performance that can be measured without a large runtime framework.

## Decision

Backend services and the search core will be implemented in Go. Model inference may run in a
separate service when vector retrieval is introduced.

## Consequences

Go provides a consistent language for network services, persistence code, indexing algorithms, and
benchmarks. Memory layout and allocation behavior require deliberate measurement, particularly in
posting-list and vector-index implementations.

