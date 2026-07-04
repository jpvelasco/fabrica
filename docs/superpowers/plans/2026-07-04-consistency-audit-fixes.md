# Consistency-Audit Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the misleading docs the Milestone-5 consistency audit found, and delete the orphaned .NET project — docs + cleanup only (architecture came back clean; no code changes).

**Architecture:** Two tasks. Task 1 = surgical doc edits (AGENTS.md de-stale, README `m7i` fix, CLAUDE.md sentinel-rule correction, ROADMAP audit line). Task 2 = delete `tests/Fabrica.Tests/` and its cascade (codecov.yml ignore line + CLAUDE.md orphan section), then full verification.

**Tech Stack:** Markdown docs, YAML (codecov.yml). No Go code changes. Go build/test only for verification.

## Global Constraints

- Doc edits are SURGICAL find/replace — match existing voice; don't restructure correct content.
- Document ONLY what the code actually does (verified): Horde default is `m7i.2xlarge` (`internal/horde/plan.go:43`); coverage gate is Codecov patch ≥90% (`codecov.yml`); `fabrica setup` is fully functional (PR #58).
- The doc-drift guard (`cmd/root/docs_drift_test.go`) must still pass — README edits are value-only, they do not remove any command mention.
- Deleting `tests/Fabrica.Tests/` is safe: the ONLY non-doc reference is `codecov.yml`'s ignore line (removed in Task 2). No Go/CI/go.mod reference exists.
- LF line endings; no CRLF. Conventional-commit messages (git hooks enforce commit-msg). Hooks active (core.hooksPath=.githooks); no `--no-verify`.
- Quality gate: `go build ./... && go test ./... && go vet ./... && gofmt -l .` clean; `golangci-lint run ./...` 0 issues.

---

### Task 1: Doc corrections (AGENTS.md, README.md, CLAUDE.md, ROADMAP.md)

**Files:**
- Modify: `AGENTS.md`, `README.md`, `CLAUDE.md`, `ROADMAP.md`

**Interfaces:**
- Consumes: nothing (docs).
- Produces: accurate docs. No code interface.

- [ ] **Step 1: AGENTS.md — replace the stale "setup not functional" limitation**

In `AGENTS.md`, find this bullet under `## Current Known Limitations`:

```markdown
- **`fabrica setup` is not yet functional.** The S3 bucket and DynamoDB lock table must be created manually before using any other Fabrica commands. Running `fabrica setup` without `--dry-run` prints a warning and exits — it does not create any AWS resources. See [docs/setup-manual.md](docs/setup-manual.md) once that document exists, or create the resources manually.
```

Replace with:

```markdown
- **State backend is created by `fabrica setup`.** `fabrica setup` provisions the S3 state bucket (versioning + encryption + public-access-block) and the DynamoDB lock table, idempotently — it shows a plan + cost estimate and prompts before any write (`--yes` skips, `--dry-run` previews). Run it once before other commands.
```

(This removes the false "not functional" claim and the dead `docs/setup-manual.md` link.)

- [ ] **Step 2: AGENTS.md — fix the coverage target**

In `AGENTS.md`, under `**Tests:**`, find:

```markdown
- Coverage target: 60%+ for `internal/*`
```

Replace with:

```markdown
- Coverage: new/changed code must meet the Codecov `patch` gate (≥90%, enforced in CI via `codecov.yml`); no new function ships at 0%
```

- [ ] **Step 3: AGENTS.md — de-stale the "Current state" line**

In `AGENTS.md`, find:

```markdown
**Current state:** Phase 0 (CLI skeleton + AWS foundation) complete. Three modules fully implemented: `perforce` (Helix Core provisioning), `horde` (build farm provisioning + job submission), and `workstation` (NICE DCV cloud workstation provisioning).
```

Replace with:

```markdown
**Current state:** Phase 0 complete; Phase 1 core complete. Modules implemented: `perforce`, `horde`, `workstation`, `ci`, `deploy`, and `cost`, plus full-stack `destroy --all` and a CLI E2E test suite. See [ROADMAP.md](ROADMAP.md) and [CLAUDE.md](CLAUDE.md) for the authoritative, current module status — this file is a high-level orientation, not a status mirror.
```

(Leave the `## Current Modules` table below as-is — it is not wrong for the three it lists, and the new line points readers to ROADMAP/CLAUDE for the full set. Do NOT expand the table into a duplicate status source that will drift.)

- [ ] **Step 4: README.md — fix the `m7i.xlarge` → `m7i.2xlarge` error (both places)**

In `README.md`, find the Horde prose (line ~141):

```markdown
Provisions an Unreal Horde build coordinator on an `m7i.xlarge` instance using your pre-baked AMI. Security group allows ports 5000 (HTTP), 5002 (gRPC), and inbound traffic from `10.0.0.0/8`. Generates MongoDB credentials to `.fabrica/horde-credentials.yaml` (mode 0600).
```

Change `m7i.xlarge` → `m7i.2xlarge` in that sentence.

Then find the config example (line ~337):

```yaml
  instance_type: m7i.xlarge
```

Change to:

```yaml
  instance_type: m7i.2xlarge
```

Verify no other `m7i.xlarge` remains: `grep -n "m7i.xlarge" README.md` must return nothing.

- [ ] **Step 5: CLAUDE.md — correct the "No sentinel errors" rule**

In `CLAUDE.md`, find (line ~210):

```markdown
**Error handling:** `fmt.Errorf("context: %w", err)`. Messages state what went wrong AND what to do. No sentinel errors.
```

Replace with:

```markdown
**Error handling:** `fmt.Errorf("context: %w", err)`. Messages state what went wrong AND what to do. Prefer wrapped context errors; do not add ad-hoc sentinels in `cmd/*` or module layers. The narrow exception is `internal/cloud`, which defines package-level sentinels (`ErrResourceNotFound`, `ErrStateBucketNotEmpty`) that callers branch on via `errors.Is` (teardown idempotency, non-empty-bucket detection).
```

- [ ] **Step 6: ROADMAP.md — mark the consistency review done**

In `ROADMAP.md`, under Milestone 5, find the architecture-review line (it reads similar to `- ⬜ Final architecture + consistency review`). Replace with:

```markdown
- ✅ Final architecture + consistency review (clean layering; doc/cleanup fixes applied; test-coverage gaps tracked as a follow-up)
```

Leave the release-prep line (v0.1 / v1.0) unchecked. If the exact text differs, match the real line — grep `grep -n "consistency review\|architecture" ROADMAP.md` first and edit that line; do NOT guess.

- [ ] **Step 7: Verify docs are internally consistent + guard still passes**

Run:
```bash
grep -n "m7i.xlarge" README.md   # expect: no output
grep -n "not yet functional\|setup-manual.md" AGENTS.md   # expect: no output
go test ./cmd/root/ -run TestEveryCommandIsDocumented   # expect: PASS (README edits are value-only)
```
Expected: first two greps empty; the drift-guard test passes. Fix any dangling reference.

- [ ] **Step 8: Commit**

```bash
git add AGENTS.md README.md CLAUDE.md ROADMAP.md
git commit -m "docs: correct stale AGENTS.md, README m7i type, sentinel-error rule (Milestone 5 audit)"
```

---

### Task 2: Delete the orphaned .NET project + cascade

**Files:**
- Delete: `tests/Fabrica.Tests/` (entire directory)
- Modify: `codecov.yml` (remove the ignore line), `CLAUDE.md` (remove the orphan section)

**Interfaces:**
- Consumes: nothing.
- Produces: a repo with no orphaned .NET project and no dangling references to it.

- [ ] **Step 1: Confirm the only non-doc reference is codecov.yml**

Run:
```bash
grep -rn "Fabrica.Tests" --include="*.go" --include="*.yml" --include="*.yaml" --include="go.mod" --include="go.sum" . | grep -v "docs/superpowers"
```
Expected: exactly one hit — `codecov.yml`. (If anything else appears, STOP and report — a build/CI dependency would change the plan.)

- [ ] **Step 2: Delete the directory**

```bash
git rm -r tests/Fabrica.Tests/
```

If `tests/` is now empty, remove it too: `rmdir tests 2>/dev/null || true` (git won't track an empty dir; nothing to commit if it only held the .NET project).

- [ ] **Step 3: Remove the codecov.yml ignore line**

In `codecov.yml`, find:

```yaml
ignore:
  - "tests/Fabrica.Tests"  # orphaned .NET project, not part of the Go build
  - "**/*_test.go"
```

Replace with:

```yaml
ignore:
  - "**/*_test.go"
```

- [ ] **Step 4: Remove the obsolete CLAUDE.md orphan section**

In `CLAUDE.md`, delete the entire section (heading + its paragraph):

```markdown
## Orphaned .NET Test Project (Ignore)

`tests/Fabrica.Tests/` is a C#/xUnit project (`Fabrica.Tests.csproj`) left over from an abandoned C# design. It references a `src/Fabrica.Cli`, `src/Fabrica.Constructs`, and `src/Fabrica.Operations` tree that **does not exist** — it will not build. Fabrica is pure Go. Do not add to it, fix it, or treat its references as real; the only test suite is the Go one (`go test ./...`).
```

Remove the heading, the paragraph, and one surrounding blank line so no double-blank gap remains.

- [ ] **Step 5: Verify nothing references the deleted project + YAML is valid**

Run:
```bash
grep -rn "Fabrica.Tests\|Orphaned .NET" . | grep -v "docs/superpowers"   # expect: no output
python -c "import yaml; yaml.safe_load(open('codecov.yml')); print('codecov.yml valid')"
ls tests/Fabrica.Tests 2>&1   # expect: No such file or directory
```
Expected: grep empty (no dangling refs anywhere but the plan/spec under docs/superpowers), codecov.yml parses, directory gone.

- [ ] **Step 6: Full quality gate**

Run:
```bash
go build ./... && go test ./... && go vet ./... && gofmt -l .
golangci-lint run ./...
```
Expected: build clean, ALL tests pass (deleting the .NET dir doesn't touch Go), vet clean, `gofmt -l` prints nothing, golangci-lint 0 issues.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "chore: delete orphaned .NET test project + drop its codecov ignore/CLAUDE.md note (Milestone 5)"
```

## Plan complete

Spec coverage:
- AGENTS.md de-stale (setup functional, 90% coverage, current-state, drop dead link) → Task 1 Steps 1-3.
- README `m7i` fix → Task 1 Step 4.
- CLAUDE.md sentinel-error rule → Task 1 Step 5.
- ROADMAP audit line → Task 1 Step 6.
- Delete .NET project + codecov.yml + CLAUDE.md orphan section cascade → Task 2.
- Verification (guard passes, no dangling refs, full gate) → Task 1 Step 7 + Task 2 Steps 5-6.
- Out-of-scope (test-coverage gaps, output-writer + receiver cosmetics, any code/arch change) untouched.
