# Contributing to Fabrica

Thanks for your interest in contributing. This guide covers the basics of
building, testing, and opening a pull request.

## Getting started

1. Fork the repository and clone your fork.
2. Create a feature branch from `main`:
   ```bash
   git checkout -b feat/your-feature
   ```
3. Install Go **1.25.12+**.
4. (Optional, once per clone) activate the tracked git hooks:
   ```bash
   git config core.hooksPath .githooks
   ```

## Build, test, lint

```bash
go build ./...
go vet ./...
go test ./...                          # Windows (no -race)
go test -race -coverprofile=coverage.out -covermode=atomic ./...  # Linux/macOS
golangci-lint run ./...
gofmt -w .
```

Single-package or single-test runs:

```bash
go test ./cmd/configcmd/ -count=1
go test ./... -run TestName
```

## Pull request process

1. Keep each PR focused on a single concern.
2. Use [Conventional Commits](https://www.conventionalcommits.org/) for messages
   (`feat:`, `fix:`, `docs:`, `test:`, `chore:`, `refactor:`, `ci:`, `build:`).
3. Write a clear PR description explaining **what** changed and **why**.
   GitHub pre-fills [`.github/PULL_REQUEST_TEMPLATE.md`](.github/PULL_REQUEST_TEMPLATE.md) —
   complete every checklist item with evidence (or **N/A + reason**). Unchecked
   applicable boxes mean the PR is not ready for review.
4. CI must pass (lint + build + test on ubuntu / windows / macos) before merge.
   Codacy must report the PR up to standards when that check runs.
5. Prefer squash-merge into `main` unless commit history is meaningful.

## Architecture notes

- New modules follow the `cmd/perforce/` + `internal/perforce/` pattern:
  pure plan layer (no AWS SDK) under `internal/<module>/`, Cobra wiring under
  `cmd/<module>/`.
- Config structs live in `internal/config/config.go` with `mapstructure:` tags.
- Tests use a two-file pattern: white-box `*_test.go` (package `<cmd>`) and
  black-box `cobra_test.go` (package `<cmd>_test`).
- Do not re-register `AWS::EC2::Instance` or `AWS::EC2::Volume` cost estimators.

See [AGENTS.md](AGENTS.md) for the full architecture and conventions, and
[CLAUDE.md](CLAUDE.md) for build commands and package responsibilities.

## Code of Conduct

By participating, you agree to abide by the [Code of Conduct](CODE_OF_CONDUCT.md).

## Security

Report vulnerabilities privately — see [SECURITY.md](SECURITY.md).
