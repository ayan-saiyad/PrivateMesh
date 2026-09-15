# Architecture

## Ownership boundary

PrivateMesh separates query orchestration from document ownership. The coordinator authenticates a
request, selects healthy owning nodes, chooses a retrieval strategy, propagates a deadline, and
merges ranked results. It holds no source-document store and builds no global index.

Each search node owns one collection in the reference deployment. It persists mutations, rebuilds
its indexes after restart, evaluates policy locally, and returns only permitted results. A node is
authoritative for its source documents, lexical postings, embeddings, and access policy.

## Request path

```text
Browser or API client
        |
        | HTTP + OIDC bearer token
        v
   Coordinator
        |
        | short-lived signed principal, deadline, gRPC stream
        +--------------------------+
        |                          |
        v                          v
Engineering node              Research node
        |                          |
        v                          v
WAL + local indexes           WAL + local indexes
```

The coordinator registry tracks node leases, collection ownership, labels, and applied log offsets.
The planner uses the request deadline, query features, and node health to select lexical, vector, or
hybrid retrieval. The distributed executor fans out concurrently and emits updated global top-k
results as nodes answer. The final HTTP response identifies both searched and unavailable shards.

## Local retrieval

The search core is transport-independent. Its lexical path tokenizes normalized Unicode text,
stores field-aware term frequencies, and ranks with BM25. The vector path supports an exact cosine
baseline and an HNSW graph. Hybrid retrieval combines independently ranked lexical and vector lists
using reciprocal-rank fusion.

The running nodes use deterministic local embeddings so the complete system has no external model
dependency. The embedding interface can be replaced by an organization-hosted model without
changing query distribution or policy enforcement.

## Durability and recovery

Every mutation is appended and committed to a checksummed WAL before the in-memory indexes change.
On startup, the node replays committed entries and ignores uncommitted work. Immutable segment
files have versioned headers, checksums, delta-encoded postings, atomic publication, and a merge
policy.

Replication primitives model a single leased primary, ordered replica acknowledgement, retained-log
catch-up, and snapshot installation after compaction. The reference two-node deployment assigns a
different collection to each node; it demonstrates independent ownership and partial availability,
not two replicas of the same collection.

## Identity and authorization

Outside demonstration mode, the coordinator verifies an OIDC access token and creates a short-lived
Ed25519 token containing the normalized principal, groups, and scopes. Search nodes verify that
token locally. They never trust browser-supplied principal headers.

Search and full-document reads pass through collection and optional document policies at the owning
node. Write RPCs require the `documents.write` scope. Authorization logs contain opaque HMAC
fingerprints and request/resource identifiers, not subject names, queries, or content.

## Observability

HTTP and gRPC spans propagate W3C trace context through the coordinator and nodes. Prometheus
metrics cover request rates, latency distributions, retrieval strategies, result counts, RPC status,
and unavailable shards. Attribute sets are bounded and deliberately exclude URLs, query text,
documents, snippets, tokens, and principal fields.

The Compose environment routes OTLP traces through the OpenTelemetry Collector to Jaeger and
provisions a Grafana dashboard backed by Prometheus and Jaeger.

## Dependency direction

```text
commands -> runtimes -> coordinator/search-node services -> search algorithms
                         |                         |
                         +-> transport            +-> WAL and segments
                         +-> identity/policy       +-> vector indexes
                         +-> telemetry
```

The algorithm packages do not depend on HTTP, gRPC, configuration, identity providers, or a
particular deployment platform.
