#!/usr/bin/env bash

set -euo pipefail

unformatted="$(gofmt -l cmd internal)"
if [[ -n "${unformatted}" ]]; then
  echo "The following Go files require formatting:"
  echo "${unformatted}"
  exit 1
fi

go vet ./cmd/... ./gen/go/... ./internal/...
go test -race -coverprofile=coverage.out ./cmd/... ./gen/go/... ./internal/...
mkdir -p bin
go build -buildvcs=false -trimpath -o bin/coordinator ./cmd/coordinator
go build -buildvcs=false -trimpath -o bin/search-node ./cmd/search-node
