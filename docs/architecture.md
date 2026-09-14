# Architecture

## System boundary

PrivateMesh separates orchestration from data ownership.

The coordinator is responsible for authenticating requests, selecting nodes, enforcing deadlines,
and merging ranked results. It must not persist source documents or build a central search index.

Search nodes ingest documents, maintain local indexes, evaluate local authorization, execute
queries, and return permitted results. A node remains authoritative for every document it owns.

## Initial process model

```text
Browser
   |
   | HTTPS
   v
Coordinator
   |
   | gRPC with deadlines and authenticated principal claims
   +-------------------+
   |                   |
   v                   v
Search node A       Search node B
   |                   |
   v                   v
Local indexes       Local indexes
```

The foundation phase exposes only process-level HTTP endpoints. Search and control-plane gRPC
services are defined as contracts but will be implemented with the distributed query phase.

## Dependency direction

The search core will contain indexing and ranking algorithms without depending on transport,
service configuration, identity providers, or a particular storage engine. Outer packages may
adapt those algorithms to HTTP, gRPC, files, or databases; the search core must not import those
outer packages.

```text
commands -> application services -> search core
                    |
                    +-> transport and persistence adapters
```

## Consistency direction

The target write model is a single elected primary per shard with WAL-based replication. Queries
may return partial results when shards are unavailable, but the response must identify missing
coverage. The exact acknowledgement and failover guarantees will be fixed before replication code
is written.

## Data classification

Source documents, query text, snippets, embeddings, access-control lists, and user identifiers are
sensitive. Logs and metrics may contain opaque request, node, shard, and collection identifiers,
but must exclude sensitive values by default.

