# Architecture

PrivateMesh has one coordinator and any number of search nodes.

The coordinator handles each request. It checks who is making the request, finds the right nodes,
sends them the search, and joins their results.

Each search node owns a document collection. It saves those documents, builds its own search data,
checks access, and returns only allowed results. The coordinator does not keep a full copy of the
documents or indexes.

## Search flow

```text
Browser or API
      |
      v
Coordinator
      |
      +-------------------+
      |                   |
      v                   v
Search node A         Search node B
      |                   |
      v                   v
Local documents       Local documents
and indexes           and indexes
```

The coordinator keeps a short list of active nodes and the collections they own. It sends work to
healthy nodes and waits only as long as the request allows. The final response lists both searched
and unavailable collections.

## Searching

Each node supports:

- Word search through a local BM25-style inverted index
- Meaning-based search through a local HNSW approximate-nearest-neighbor index
- A mix of both through reciprocal-rank fusion

The local demo creates deterministic meaning data on the same machine, so it does not need an
outside AI service. That embedder can be replaced with another model later.

## Saving and recovery

A node saves each accepted change before adding it to the live index. After a restart, it reads the
saved changes and rebuilds what it needs. Saved files include checks that catch damaged data.

The project also includes the main pieces needed to copy changes to another node, catch up an older
copy, and restore from a saved snapshot. In the local demo, the two nodes own different collections.

## Login and access

In a real deployment, the coordinator checks the user's login token. It then creates a short-lived
signed token for the search nodes. Each node checks that token and its own access rules before
returning a result or document.

Write requests need the `documents.write` permission. Audit logs use scrambled IDs and do not save
user names, searches, or document text.

## Monitoring

The services report request counts, timing, errors, missing collections, and traces. They do not put
search text, document text, login tokens, or user details in those reports.

The local Docker setup sends metrics to Prometheus, dashboards to Grafana, and traces to Jaeger.

## Code layout

Search code stays separate from the HTTP and gRPC code. This keeps the main search behavior easy to
test and lets the app change its network or storage code without rewriting ranking.
