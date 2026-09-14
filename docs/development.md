# Development environment

## Supported path

The supported development environment uses Docker so compiler, linter, and protocol-tool versions
remain consistent across machines and CI.

Pinned tools:

| Tool | Version | Purpose |
|---|---:|---|
| Go | 1.26.5 | Backend services and search core |
| Node.js | 24.21.0 LTS | Web client toolchain |
| Buf | 1.72.0 | Protobuf linting and generation |
| golangci-lint | 2.12.2 | Go static analysis |

## First setup

```bash
cp .env.example .env
make init
```

`make init` installs locked web dependencies into a Docker volume and then runs all quality gates.
Keeping container dependencies in that volume prevents Linux packages from overwriting a local
macOS `node_modules` directory.

## Common commands

| Command | Effect |
|---|---|
| `make fmt` | Format Go and web source files |
| `make test` | Run backend tests with the race detector and frontend tests |
| `make lint` | Run Go, TypeScript, and protobuf linters |
| `make proto` | Regenerate Go protocol bindings |
| `make build` | Build backend binaries and the frontend bundle |
| `make check` | Run the complete local quality suite |
| `make dev` | Run the coordinator and two search nodes |
| `make down` | Stop the local environment without deleting node data |

## Configuration

Backend configuration uses environment variables prefixed with `PRIVATEMESH_`. Copy
`.env.example` to `.env` for local overrides. The `.env` file is ignored by Git.

Configuration is validated during process startup. Invalid addresses, durations, and log levels
must prevent a service from starting.

## Local ports

| Process | Port |
|---|---:|
| Coordinator | 18080 |
| Search node A | 18091 |
| Search node B | 18092 |
| Vite development server | 3000 |

These are host ports. Containers continue to listen on `8080` for the coordinator and `8090` for
search nodes. Host-port overrides are available in `.env.example`.

## Protocol changes

Edit files under `proto/privatemesh/v1`, run `make proto`, then run `make check`. Once the first
released protocol exists, CI will compare changes against the `main` branch with Buf's breaking
change detector.
