## Summary

Brief description of **what** this PR does and **why**.

## Changes

- Change 1
- Change 2

## Testing

Mark each box only when verified with evidence (CI job, local command output, or N/A with reason).

- [ ] Tests pass locally (`go test ./...` or scoped packages you touched)
- [ ] Tested on: <!-- e.g. Windows 11 / pwsh, Ubuntu 24.04 -->
- [ ] CI green on this PR (lint, build, test matrix, gosec, CodeQL, Codacy as applicable)
- [ ] Codacy PR quality gate clean (`isUpToStandards` / 0 new issues blocking merge) — or N/A if Codacy did not run
- [ ] Codecov: `codecov/patch` status (and/or `codecov[bot]` comment) present after Test (ubuntu) — or N/A if no coverable Go changes / upload skipped
- [ ] New/changed code covered by tests (Codecov patch ≥90% or E2E-only with justification)

### Scope-specific (check only the boxes that apply; leave unchecked with reason if not applicable)

- [ ] `go test ./internal/<module>/...` and/or `go test ./cmd/<module>/...` for packages touched
- [ ] `cd npm && npm test` — if `npm/` changed
- [ ] `golangci-lint run ./...` — if you want local lint parity before push (CI also runs it)

## Documentation

Use **N/A + reason** when the box does not apply (do not leave applicable boxes unchecked).

- [ ] Updated `README.md` (user-facing commands, flags, install) — or N/A: …
- [ ] Updated `CLAUDE.md` / `AGENTS.md` (architecture, conventions, build commands) — or N/A: …
- [ ] Updated `ROADMAP.md` / module docs under `docs/` — or N/A: …

## Test plan

How a reviewer can verify this change. Prefer concrete commands and expected outcomes.

1. …
2. …

## Related Issues

Closes #
