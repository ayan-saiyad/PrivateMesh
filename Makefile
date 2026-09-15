SHELL := /bin/bash

GO_VERSION := 1.26.5
NODE_VERSION := 24.21.0
BUF_VERSION := 1.72.0
GOLANGCI_LINT_VERSION := v2.12.2
ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
GO_IMAGE := golang:$(GO_VERSION)-bookworm
NODE_IMAGE := node:$(NODE_VERSION)-bookworm-slim
WEB_NODE_MODULES_VOLUME := privatemesh-web-node-modules
NODE_RUN := docker run --rm -v "$(ROOT_DIR)/web:/workspace" -v "$(WEB_NODE_MODULES_VOLUME):/workspace/node_modules" -w /workspace $(NODE_IMAGE)
BENCHMARK_DOCUMENTS ?= 1000000
BENCHMARK_QUERIES ?= 500
LOAD_TEST_URL ?= http://host.docker.internal:18080/api/search
LOAD_TEST_REQUESTS ?= 1000
LOAD_TEST_CONCURRENCY ?= 25

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available commands.
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\n"} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: init
init: ## Install web dependencies and validate the toolchain.
	$(NODE_RUN) npm ci
	$(MAKE) check

.PHONY: fmt
fmt: ## Format Go and web source files.
	docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace $(GO_IMAGE) gofmt -w cmd internal
	$(NODE_RUN) npm run format

.PHONY: fmt-check
fmt-check: ## Verify source formatting without modifying files.
	@test -z "$$(docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace $(GO_IMAGE) gofmt -l cmd internal)"
	$(NODE_RUN) npm run format:check

.PHONY: test
test: ## Run backend and frontend tests.
	docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace $(GO_IMAGE) go test -race -coverprofile=coverage.out ./cmd/... ./gen/go/... ./internal/...
	$(NODE_RUN) npm test

.PHONY: vet
vet: ## Run the Go static analyzer.
	docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace $(GO_IMAGE) go vet ./cmd/... ./gen/go/... ./internal/...

.PHONY: lint
lint: ## Run Go, TypeScript, and protobuf linters.
	docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace golangci/golangci-lint:$(GOLANGCI_LINT_VERSION) golangci-lint run ./cmd/... ./internal/...
	$(NODE_RUN) npm run lint
	docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace bufbuild/buf:$(BUF_VERSION) lint

.PHONY: proto
proto: ## Generate Go bindings from protobuf contracts.
	docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace bufbuild/buf:$(BUF_VERSION) generate
	docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace $(GO_IMAGE) go mod tidy

.PHONY: build
build: ## Build service binaries and the web client.
	docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace $(GO_IMAGE) sh -c 'mkdir -p bin && go build -trimpath -o bin/coordinator ./cmd/coordinator && go build -trimpath -o bin/search-node ./cmd/search-node'
	$(NODE_RUN) npm run build

.PHONY: check
check: fmt-check vet lint test build compose-config ## Run all local quality gates.

.PHONY: dev
dev: ## Build and run the local multi-service environment.
	docker compose -f deployments/compose/compose.yaml up --build

.PHONY: down
down: ## Stop the local environment and retain node data.
	docker compose -f deployments/compose/compose.yaml down

.PHONY: clean
clean: ## Remove build outputs without deleting runtime data.
	docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace $(GO_IMAGE) sh -c 'rm -rf bin coverage.out coverage.html'
	$(NODE_RUN) sh -c 'rm -rf dist coverage'

.PHONY: compose-config
compose-config: ## Validate the Compose model.
	docker compose -f deployments/compose/compose.yaml config --quiet

.PHONY: benchmark-million
benchmark-million: ## Index one million deterministic documents and report latency and memory use.
	docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace $(GO_IMAGE) go run ./cmd/index-benchmark -documents $(BENCHMARK_DOCUMENTS) -queries $(BENCHMARK_QUERIES)

.PHONY: load-test
load-test: ## Exercise the running search API under concurrent load.
	docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace $(GO_IMAGE) go run ./cmd/load-test -url $(LOAD_TEST_URL) -requests $(LOAD_TEST_REQUESTS) -concurrency $(LOAD_TEST_CONCURRENCY)

.PHONY: chaos-test
chaos-test: ## Verify explicit partial results and node rejoining after process loss.
	./scripts/chaos-test.sh

.PHONY: recovery-test
recovery-test: ## Verify WAL recovery across a search-node restart.
	./scripts/recovery-test.sh
