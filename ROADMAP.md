# Project status

The work planned for version 1.0.0 is complete.

## Search and saved data

- [x] Search by words
- [x] Search by meaning
- [x] Combined search and result ranking
- [x] Local document indexes
- [x] Saved writes and restart recovery
- [x] Checks for damaged saved data

## Multiple nodes and access

- [x] Track active search nodes and their collections
- [x] Search several nodes at once
- [x] Stop slow work when a request times out
- [x] Report collections that could not be searched
- [x] Choose a healthy copy and bring older copies up to date
- [x] Check user login tokens
- [x] Apply collection and document access rules on each node
- [x] Keep audit logs without saving private content

## App and deployment

- [x] Browser search app and API
- [x] Metrics, traces, Grafana, and Jaeger
- [x] Load, node failure, and restart checks
- [x] Repeatable one-million-document benchmark
- [x] Docker Compose and Kubernetes setup
- [x] Automatic checks and versioned container releases
