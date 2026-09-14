# 0002: Keep the search core independent

- Status: accepted
- Date: 2026-09-11

## Context

Indexing and ranking algorithms need deterministic unit tests and benchmarks. Coupling them to a
web framework, RPC implementation, database, or model provider would make correctness and
performance harder to isolate.

## Decision

The search core will expose Go data types and interfaces without importing HTTP, gRPC, identity,
database, or model-client packages. Adapters at the application boundary will translate external
requests and persistence formats.

## Consequences

The system requires explicit adapters, but algorithms can be tested in memory and benchmarked
without network or storage noise. Replacing a transport or persistence implementation will not
rewrite ranking logic.

