# Development environment

The supported toolchain runs through Docker so local checks and CI use the same compiler, linter,
protocol generator, and frontend runtime.

| Tool | Version | Purpose |
|---|---:|---|
| Go | 1.26.5 | Services, search core, tests, and operational tools |
| Node.js | 24.21.0 | Web client toolchain |
| Buf | 1.72.0 | Protobuf linting and generation |
| golangci-lint | 2.12.2 | Go static analysis |

## Setup

```bash
cp .env.example .env
make init
```

`make init` installs locked web dependencies into a Docker volume and runs the complete quality
gate. Keeping those dependencies in a Linux volume avoids overwriting a local macOS
`node_modules` directory.

## Commands

| Command | Effect |
|---|---|
| `make fmt` | Format Go and web sources |
| `make test` | Run Go tests with the race detector and frontend tests |
| `make lint` | Run Go, TypeScript, and protobuf linters |
| `make proto` | Regenerate Go protocol bindings and tidy modules |
| `make build` | Build both services and the web bundle |
| `make check` | Run formatting, vet, lint, tests, builds, and Compose validation |
| `make dev` | Build and run the complete local environment in the foreground |
| `make down` | Stop it without deleting persistent data |
| `make load-test` | Run concurrent searches against the local coordinator |
| `make chaos-test` | Verify partial results during node loss and recovery |
| `make recovery-test` | Verify a committed document survives restart |
| `make benchmark-million` | Run the deterministic index benchmark |
| `make kubernetes-config` | Render both Kubernetes overlays |

The load and benchmark counts can be overridden without editing files:

```bash
make load-test LOAD_TEST_REQUESTS=5000 LOAD_TEST_CONCURRENCY=50
make benchmark-million BENCHMARK_DOCUMENTS=250000 BENCHMARK_QUERIES=100
```

## Local ports

| Process | Port |
|---|---:|
| Web | 18000 |
| Coordinator HTTP / gRPC | 18080 / 18081 |
| Search node A HTTP / gRPC | 18091 / 18093 |
| Search node B HTTP / gRPC | 18092 / 18094 |
| Grafana | 13000 |
| Prometheus | 19090 |
| Jaeger | 16686 |

Host ports can be changed in `.env`. Containers keep their fixed internal ports.

## Protocol changes

Edit `proto/privatemesh/v1`, run `make proto`, and then run `make check`. Generated bindings are
committed so CI can reject stale protocol output.
