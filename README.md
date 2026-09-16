# PrivateMesh

PrivateMesh lets several teams search their own document collections from one place. Each search
node keeps its documents and search data. A coordinator sends each search to the right nodes and
combines their answers.

If one node is offline, the available nodes can still return results. The response also says which
parts could not be searched.

## Quick start

You need Docker Desktop with Compose v2 and GNU Make.

```bash
docker compose -f deployments/compose/compose.yaml up -d --build
```

Open the search app at http://127.0.0.1:18000. The local setup includes two search nodes and a few
sample documents. Try `distributed search`, `replica recovery`, or `local ownership`.

Other local pages:

| Service | URL |
|---|---|
| API | http://127.0.0.1:18080 |
| Grafana | http://127.0.0.1:13000/d/privatemesh-overview/privatemesh-overview |
| Prometheus | http://127.0.0.1:19090 |
| Jaeger | http://127.0.0.1:16686 |

You can also search through the API:

```bash
curl --fail --silent --show-error \
  -H 'Content-Type: application/json' \
  --data '{"query":"distributed search","mode":"auto","limit":5}' \
  http://127.0.0.1:18080/api/search
```

Stop the containers without deleting their saved data:

```bash
make down
```

## What it includes

- Word, meaning-based, and combined search
- Fast local indexes for each document collection
- Saved writes and recovery after a restart
- Search across several nodes at once
- Clear partial results when a node is unavailable
- Login checks and access rules on each node
- Metrics, traces, dashboards, and recovery checks
- A React search app, Docker setup, and Kubernetes files

## Useful checks

```bash
make check
make load-test
make chaos-test
make recovery-test
make benchmark-million
make kubernetes-config
```

## Important limits

The coordinator sees the search text and allowed result previews while handling a request. A search
node also sees any query sent to it. PrivateMesh does not hide queries from those services.

The included setup is for local use. A real deployment needs a login provider, new signing keys,
encrypted connections, and protected storage.

More details are in the [architecture](docs/architecture.md), [security notes](docs/threat-model.md),
[development guide](docs/development.md), and [operations guide](docs/operations.md).
