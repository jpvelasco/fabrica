# Design: Make `fabrica setup` an Honest No-Op

**Date:** 2026-05-23
**Status:** Approved

## Problem

`internal/state/bootstrap.go` contains five fully stubbed functions that return hardcoded success without making any AWS calls. As a result, `fabrica setup` silently reports "Setup complete" while doing nothing to the actual S3 bucket or DynamoDB lock table. This is a correctness bug: the tool lies to the user.

## Goal

Make `fabrica setup` honest. Until the real AWS calls are wired, the command must loudly and clearly communicate that it is not functional, in both dry-run and apply modes.

## Approach

Replace the silent stub behavior with an explicit sentinel error. The `Bootstrap` function returns `ErrBootstrapNotImplemented`. The setup command detects that error and prints a prominent warning instead of reporting false success.

The dry-run path is also updated to note that the creation step is not yet automated, so users are not misled even in preview mode.

## Changes

### `internal/state/bootstrap.go`

- Export a sentinel: `var ErrBootstrapNotImplemented = errors.New("...")`
- `Bootstrap` returns `nil, ErrBootstrapNotImplemented` immediately.
- Remove the five dead inner stubs (`createBucket`, `enableVersioning`, `blockPublicAccess`, `enableEncryption`, `createTable`). They are unreachable once `Bootstrap` short-circuits.
- `BootstrapResult` type is retained for when the real implementation lands.

### `cmd/setup/setup.go`

- `runApply` detects `errors.Is(err, fabricastate.ErrBootstrapNotImplemented)` and prints a prominent block:

  ```
  WARNING: fabrica setup is not yet functional.

  The S3 bucket and DynamoDB table must be created manually
  before using Fabrica. Fabrica has NOT created or modified
  any AWS resources.

  Manual setup instructions: docs/setup-manual.md
  ```

  Then returns nil (not an error) so the exit code is 0 but the output is unmistakable.

- `printDryRun` appends a notice below the "Run without --dry-run" line:

  ```
  NOTE: Automated provisioning is not yet implemented.
  Resources above must be created manually.
  ```

- The `Long` help description is updated to state that automated creation is not yet available.

### `cmd/setup/setup_test.go`

Two new tests (white-box, seam-injected):

- `TestRunApplyPrintsNotImplementedWarning`: inject a bootstrap seam returning `ErrBootstrapNotImplemented`, assert output contains the WARNING block and does NOT contain "Setup complete".
- `TestDryRunShowsNotImplementedNotice`: call `printDryRun` and assert output contains the NOTE line.

Existing tests are unaffected.

## What Does NOT Change

- `SetupPlan`, `NewSetupPlan`, `BackendNames`, `ResolveBackendNames` — correct and useful, untouched.
- The dry-run planning output (account/region/resources/cost) — still valuable.
- `BootstrapResult` type — kept for future use.

## Success Criteria

- `fabrica setup --dry-run` shows the planning output AND the NOT YET IMPLEMENTED note.
- `fabrica setup` prints the WARNING block and exits 0 (no panic, no false success message).
- `go build ./...`, `go vet ./...`, `go test ./...` all pass.
- No existing tests are broken.
