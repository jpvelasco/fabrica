# Fabrica developer tasks. Thin wrappers over the canonical `go` commands so
# local runs, git hooks, and CI stay in sync. See CLAUDE.md for the full list.

# Version metadata injected into release builds via ldflags.
VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/jpvelasco/fabrica/internal/version.Version=$(VERSION) \
           -X github.com/jpvelasco/fabrica/internal/version.Commit=$(COMMIT)

.DEFAULT_GOAL := help

.PHONY: help
help: ## List available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build all packages (dev version)
	go build ./...

.PHONY: release
release: ## Build the CLI with version ldflags (override with VERSION=vX.Y.Z)
	go build -ldflags "$(LDFLAGS)" .

.PHONY: test
test: ## Run tests with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run tests with coverage and print the summary
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run ./...

.PHONY: fmt
fmt: ## Format all Go files
	gofmt -w .

.PHONY: vuln
vuln: ## Scan for known vulnerabilities (matches CI)
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	go mod tidy

.PHONY: ci
ci: lint vet build test ## Run the full local gate (lint + vet + build + test)
