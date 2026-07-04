# README Refresh + Doc-Drift Guard — Design (Phase 1, Milestone 5, Sub-project C)

Status: approved for implementation
Date: 2026-07-04

## Goal

Make `README.md` accurate against the shipped CLI, and add a test that keeps it
accurate so it can't silently drift again.

## Context — what's actually stale

The README is structurally sound and mostly current (it already covers most
modules), but it is ~3 milestones behind on specific points. Verified against
the code on 2026-07-04:

1. **"Current Status" prose** (README.md ~line 19): says *"Phase 0 complete.
   Perforce, Horde, and Workstation modules are implemented."* — pre-dates CI,
   deploy, cost, and destroy --all. The status table right below it already
   contradicts this line.
2. **Status table** (~line 24): `cost` row is marked **Planned** — but cost
   shipped (Milestone 4, PR #64). The `ci` row omits `destroy`. There is no
   `destroy --all` row.
3. **Commands section**: has no `### Cost` section at all (`report`/`forecast`/
   `alerts` undocumented); the `ci` subsection stops at `logs` (no `ci destroy`,
   shipped #65); there is no `destroy --all` entry (shipped #65).

Everything else (Perforce, Horde, Workstation, Deploy, CI setup/trigger/status/
logs, getting-started, requirements, building) is current.

## Decisions (locked during brainstorming)

1. **Scope:** README accuracy patch — surgical edits, not a rewrite and not new
   per-module docs/ guides. The README's structure and voice are good.
2. **Accuracy mechanism:** a **doc-drift CI guard** (a Go test) in addition to a
   one-time cross-check of every documented command against `--help`.

## Part 1 — README accuracy patch

Surgical edits to `README.md` only. Voice/structure unchanged.

1. **Current Status prose** — replace the stale sentence with one that states the
   real breadth: Phase 0 complete; Phase 1 core complete — all provisioning
   modules (Perforce, Horde, Workstation), CI, Deploy, and Cost, plus full-stack
   `destroy --all` and a CLI E2E test suite. Point to ROADMAP for phase detail.
2. **Status table** — `cost` row: **Planned → Complete**; `ci` row: add `destroy`
   to its command list; add a `destroy --all` row (Complete — full-stack
   teardown).
3. **Commands section** — add the missing entries, matching the existing
   subsection format (a `####` heading + a one/two-line description + the key
   flags each command actually has):
   - New `### Cost` section with `#### fabrica cost report`,
     `#### fabrica cost forecast`, `#### fabrica cost alerts` (list/set/check).
   - `#### fabrica ci destroy` under the existing `### CI` section.
   - `#### fabrica destroy --all` (under the `### Other` section or a new
     `### Teardown` heading — implementer's call to match surrounding style).
4. **Verify-against-`--help`:** while editing, cross-check EVERY command and the
   flags mentioned for it against actual `fabrica <cmd> --help` output. Correct
   any flag-level drift found in existing sections too — this is a correctness
   pass, not just an additive one. Do not invent flags; document only what
   `--help` shows.

No changes to CLAUDE.md or ROADMAP.md in this sub-project (both already current;
ROADMAP's M5 docs line is checked in Part 3).

## Part 2 — doc-drift guard (a Go test)

A pure Go test — no AWS, no network, no `//go:build` tag → runs in the default
`go test ./...` and the free CI test job.

**Location:** `cmd/root/docs_drift_test.go`, `package root_test` (it needs
`root.New`, which returns the fully-wired `*cobra.Command`). Reads the repo
`README.md` via a relative path from the test's working directory (the package
dir); walk up to the repo root to find it (`../../README.md` from `cmd/root/`).

**What it does:**

1. Build the command tree: `rootCmd := root.New(io.Discard)`.
2. Recursively walk `rootCmd.Commands()` collecting every **leaf** command's full
   path (space-joined, e.g. `cost report`, `ci destroy`, `deploy promote`,
   `workstation terminate`). A leaf = a command with no subcommands of its own
   AND that is runnable (`Runnable()` / has a `RunE`).
3. Skip cobra built-ins and non-documentable commands: `help`, `completion` (and
   anything under them). Also skip pure parent commands (they have subcommands;
   only their leaves are documented).
4. Read `README.md`; for each leaf path, assert the README contains it (the
   command path string, e.g. `fabrica cost report` or `cost report` — match the
   form the README uses; the test normalizes by checking for the path with and
   without the `fabrica ` prefix).
5. If any leaf command is undocumented, `t.Errorf` listing all missing ones →
   the test fails at PR time, catching "shipped a command without documenting
   it."

**Deliberately one-directional:** command → documented-in-README. It does NOT
assert README→command (prose/flag text is too fuzzy to machine-check without
false positives). The reverse (a documented command that no longer exists) stays
the manual `--help` cross-check's job.

**Special cases the test must encode explicitly (not silently skip):**
- `destroy --all` is a *flag on* `destroy`, not a subcommand — the tree walk
  finds `destroy` (a leaf). The test asserts `destroy` is documented; the
  `--all` prose is covered by the manual pass. Document this in a code comment so
  a future reader knows `--all` isn't machine-checked.
- `config show` / `config` — whatever leaves the config command exposes must be
  documented; the walk handles this generically.
- `version` is a leaf → must be documented (README already has it).

## Part 3 — docs

- ROADMAP Milestone 5: check the "Comprehensive documentation and examples" line
  (or note the README refresh + drift guard shipped). Leave the consistency-
  review and release-prep lines unchecked.

## Testing / verification

- The drift guard IS a test; it must pass (i.e. after Part 1, every leaf command
  is documented).
- `go build ./... && go test ./... && go vet ./... && gofmt -l .` clean;
  `golangci-lint run ./...` 0 issues.
- The guard is a `_test.go` file (test code), exempt from the ≥90% patch gate,
  but it must itself be meaningful — it asserts real behavior (fails when a
  command is undocumented), not a no-op.
- Manual: run `fabrica <cmd> --help` for each documented command and confirm the
  README matches (the Part-1 verification).
- Dependency rule unaffected: the test lives in `package root_test` and imports
  only `cmd/root` + stdlib.

## Out of scope (YAGNI)

- Per-module how-to guides under `docs/` (the tight patch was chosen).
- Rewriting or restructuring the README's already-current sections.
- Flag-level machine assertions (README→CLI direction) — fuzzy; handled by the
  manual `--help` cross-check instead.
- CLAUDE.md changes (already current after B).

## Docs / roadmap updates on completion

- ROADMAP Milestone 5 documentation line → ✅ (README refresh + drift guard).
