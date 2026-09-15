# Threat model

## Protected assets

- Source documents, extracted text, snippets, and embeddings
- Search queries and ranked result sets
- Collection and document policies
- OIDC tokens, principal signing keys, and audit keys
- Local index segments, write-ahead logs, and snapshots

## Trust boundaries

Each search node is trusted by the organization that operates and owns its collection. The
coordinator is trusted to authenticate requests, route queries, and merge permitted results, but it
is not a permanent document store. Every network hop and persistent volume crosses a deployment
trust boundary.

The system considers attackers that send malformed or oversized requests, replay observed
credentials, register a misbehaving node, observe an unprotected network, terminate a process, or
read application logs and metrics.

## Implemented controls

- Strict request limits, parsing, deadlines, and cancellation
- OIDC issuer/audience/signature verification at the coordinator
- Short-lived Ed25519 principal tokens with issuer and audience binding
- Node-local default-deny policy evaluation before content leaves the owner
- Required write scopes for mutation RPCs
- Checksummed, versioned segment and WAL formats with bounded record sizes
- Commit-before-apply mutation ordering and idempotent operation handling
- HMAC fingerprints for subject and resource audit fields
- Fixed-cardinality telemetry attributes that exclude protected payloads
- Non-root, capability-free, read-only containers in the Kubernetes manifests
- Explicit missing-shard coverage instead of silently presenting partial results as complete

## Deployment requirements

The committed Compose configuration is for local demonstration only. Production operators must:

- Terminate external TLS at a trusted ingress
- Encrypt and authenticate service-to-service gRPC with the cluster network or a service mesh
- Store signing and audit keys in a managed secret system and rotate them under change control
- Encrypt node volumes and backups
- Restrict coordinator, node, metrics, and tracing endpoints with network policy
- Disable demonstration mode and configure a trusted OIDC issuer and client audience

The application currently signs principals but does not establish transport TLS itself. A
production deployment without an authenticated-encryption layer does not meet this threat model.

## Explicit limitations

PrivateMesh does not provide private-information retrieval, query obliviousness, or protection from
an authorized user who copies a returned document. Selected nodes necessarily receive query text.
Embeddings receive the same protection as source content. A malicious owning node can omit or
misrank its own results; cross-node verifiable ranking is outside the current design.
