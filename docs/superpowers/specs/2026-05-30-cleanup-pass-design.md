# Cleanup Pass Design — 2026-05-30

## Goal

Remove dead code, fix a documentation/default mismatch, and normalize error messages. No architectural changes.

## Changes

### 1. Delete `src/`

The `src/` directory contains an abandoned C# CDK exploration (~84 lines across four projects). It has no Go integration and is superseded by the Go implementation. Delete entirely.

### 2. Fix instance type mismatch in `cmd/horde/create`

`internal/horde/plan.go` defaults to `m7i.2xlarge`. The help text in `cmd/horde/create/create.go` incorrectly states `m7i.xlarge`. Update the help text to match the actual default.

### 3. Standardize provider nil error messages

Create commands use: `"no provider configured; run 'fabrica setup' first"`
Destroy commands use: `"no provider configured; re-run after 'fabrica setup'"`

Update `cmd/perforce/destroy` and `cmd/horde/destroy` to use the create-command phrasing so all six commands are consistent.

### 4. Add `horde ami build` to `AGENTS.md`

Add a one-line entry for `horde ami build` in the command listing section.

## Out of Scope

- README.md — already up to date (documents `horde ami build` and Current Status)
- Nil provider guards — already present and correct in all commands
- Unused imports — clean
- Cost estimators — m7i types registered correctly in `internal/perforce/cost.go` per design

## Verification

- `go build ./...`
- `go vet ./...`
- `go test ./...`
- Confirm `src/` is gone from repo root
