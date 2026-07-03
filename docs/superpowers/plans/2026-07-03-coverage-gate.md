# Coverage Gate Hardening — Plan

**Goal:** Make new code that lacks tests fail a gate, so the 5-functions-at-0% miss can't recur — enforced primarily by CI (unbypassable), with a local pre-push hook as an early catch.

**Context (root causes of the miss):**
1. Codecov's `patch` gate (target 80%) was real and working, but I misread its cosmetic "fail 1s" line and ignored it — the branch merged-ready at 73% diff before I caught it.
2. The tracked `.githooks/` pre-commit (gofmt+vet) was **never active** in this clone: `core.hooksPath` is the default `.git/hooks`, not `.githooks`. So an unformatted commit slipped through.
3. Per-task TDD used seams that let real wiring go 0%-covered while "tests passed."

**Decisions (from JP):** no-0% + ~90% floor on changed code; enforce via CI gate + local hook (no GitHub Pro).

## Changes

### 1. `codecov.yml` — raise patch target 80% → 90%
The diff-coverage gate already exists and works. Raise `coverage.status.patch.default.target` from `80%` to `90%`. This is the load-bearing enforcement: runs on every PR via CI, cannot be bypassed locally. A new 0% function drags patch well under 90% → red check.
Keep `project` gate as-is (`auto`, threshold 1%).

### 2. `.githooks/pre-push` — local early catch
New tracked hook that runs the race-free coverage build and fails if any **changed** Go file has a function at 0.0%. It is an early warning (before push), NOT the authority — CI is. Written defensively: skips cleanly if not on a feature branch / no Go changes; never blocks a docs-only push.

Logic:
- Determine changed non-test .go files vs `origin/main` (merge-base). If none, exit 0.
- `go test -coverprofile` over the affected packages.
- Parse `go tool cover -func`; if any function in a changed file is `0.0%`, print them and exit 1 with a clear message (and how to bypass in a true emergency: `git push --no-verify`).
- Windows/Git-Bash compatible (this is the dev environment).

### 3. Activate hooks + document loudly
- Run `git config core.hooksPath .githooks` in this clone now (fixes the inactive-hooks bug immediately).
- CLAUDE.md "Git Hooks" section: add `pre-push` to the list; make the activation step a prominent one-time REQUIRED step; note that CI (Codecov patch 90%) is the real gate and the hook is a convenience.

### 4. CLAUDE.md — coverage standard + SDD rule
- Change the "Coverage target" line from `60%+ for internal/*` to: **new/changed code must meet Codecov patch ≥90%; no new function ships at 0% coverage; strive for 100%.** Keep the "mocked SDK, no real AWS" note.
- Add an SDD reviewer rule (near Test Strategy): *"Every new exported function must be executed by a test. If a task introduces a seam (a func field stubbed in tests), a test must still exercise the real non-seam path — a stubbed seam hides its own wiring from coverage."* (This is the exact trap that produced the 0% NewTeardown/RunOrchestrated functions.)

## Out of scope
- A separate CI "no-0%" script — redundant with Codecov patch 90% (which already fails a 0% new function). Not adding brittle duplication.
- GitHub Pro / branch protection (JP chose CI-gate + hook; a red required check needs Pro — deferred).

## Verification
- `codecov.yml` valid (YAML parses; Codecov re-reads on next PR).
- pre-push hook: dry-run it locally against HEAD (should pass — main is green); simulate a 0% function to confirm it fails, then revert.
- `go build ./... && go test ./... && gofmt -l . && golangci-lint run ./...` clean.
- Commit; open follow-up PR; confirm CI green + codecov patch reads the new 90% target.
