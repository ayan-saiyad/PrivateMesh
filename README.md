# PrivateMesh

PrivateMesh improves data ownership by letting documents remain on their origianl nodes, therefore not giving up ownership of those docuements. Each location keeps its files and search locally while the system coordinates the search and works even if some machines are offline.

STILL IN DEVELOPEMENT: REST OF THIS IS TENTATIVE

## Design goals

- Local ownership of source documents and indexes
- Lexical, vector, and hybrid retrieval
- Explicit partial results when nodes or shards are unavailable
- Measurable relevance, latency, and recovery behavior
- Authorization enforcement at the node that owns the document
- Reproducible builds, tests, and benchmarks

## Requirements

- Docker Desktop with Compose v2
- GNU Make
- Optional local installations: Go 1.26.5 and Node.js 22.12 or newer


