# Threat model

## Protected assets

- Source document contents
- Extracted text and snippets
- Search queries
- Embedding vectors
- Document and collection access-control lists
- User and node credentials
- Index and write-ahead-log files

## Trust boundaries

Each search node is trusted by the organization operating that node. The coordinator is trusted to
route authorized requests and merge permitted results, but is not trusted as a permanent document
store. Network links cross a trust boundary and will require authenticated encryption.

## Initial attacker capabilities

The design considers an attacker who can:

- Send malformed or oversized requests
- Replay previously observed requests
- Operate a registered but misbehaving node
- Observe or alter unprotected network traffic
- Cause a node or coordinator process to terminate
- Obtain read access to application logs

## Required controls

- Mutual authentication between backend services
- Short-lived user and service credentials
- Local authorization before a node returns content
- Request size, concurrency, and time limits
- Strict parsing of index and protocol data
- Encryption for network traffic and persistent sensitive data
- Redaction by construction rather than post-processing logs
- Auditable authorization outcomes without recording protected content

## Explicit limitations

PrivateMesh does not initially provide private-information retrieval, query obliviousness, or
protection from an authorized user who intentionally copies a returned document. Embeddings are
not anonymized and must receive the same protection as source text. A malicious node can omit or
misrank its own results; cross-node verifiable ranking is outside the initial scope.

