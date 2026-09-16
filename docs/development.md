# Development

The development tools run through Docker, so you do not need to install each one yourself.

| Tool | Version | Used for |
|---|---:|---|
| Go | 1.26.5 | Backend code and tests |
| Node.js | 24.21.0 | Web app |
| Buf | 1.72.0 | API files |
| golangci-lint | 2.12.2 | Go code checks |

## Setup

```bash
cp .env.example .env
make init
```

This installs the web packages and runs the project checks. Web packages stay in a Docker volume,
so they do not replace packages already installed on your computer.

## Commands

| Command | What it does |
|---|---|
| `make fmt` | Format the code |
| `make test` | Run backend and web tests |
| `make lint` | Check code and API files |
| `make proto` | Rebuild generated Go API files |
| `make build` | Build the services and web app |
| `make check` | Run all regular project checks |
| `make dev` | Start the full local setup |
| `make down` | Stop it without deleting saved data |
| `make load-test` | Send many searches to the local API |
| `make chaos-test` | Check behavior when a node goes offline |
| `make recovery-test` | Check that saved data survives a restart |
| `make benchmark-million` | Test an index with one million documents |
| `make kubernetes-config` | Check the Kubernetes files |

You can change the size of load and benchmark runs:

```bash
make load-test LOAD_TEST_REQUESTS=5000 LOAD_TEST_CONCURRENCY=50
make benchmark-million BENCHMARK_DOCUMENTS=250000 BENCHMARK_QUERIES=100
```

## Local ports

| Service | Port |
|---|---:|
| Web app | 18000 |
| Coordinator HTTP / gRPC | 18080 / 18081 |
| Search node A HTTP / gRPC | 18091 / 18093 |
| Search node B HTTP / gRPC | 18092 / 18094 |
| Grafana | 13000 |
| Prometheus | 19090 |
| Jaeger | 16686 |

You can change host ports in `.env`.

## Changing the API files

Edit `proto/privatemesh/v1`, run `make proto`, and then run `make check`. Commit the updated files
under `gen/go` with your change.
