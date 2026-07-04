# README Refresh + Doc-Drift Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make README.md accurate against the shipped CLI, and add a Go test that fails when a leaf command isn't documented — so the README can't silently drift again.

**Architecture:** Two tasks. Task 1 patches README.md (correct stale status, add the missing `cost` / `ci destroy` / `destroy --all` command sections), verified against real `--help`. Task 2 adds a pure Go test in `package root_test` that walks the cobra command tree from `root.New(io.Discard)` and asserts every runnable leaf command's path appears in README.md; then updates ROADMAP.

**Tech Stack:** Go 1.25.11, Cobra, standard `testing`. No new dependencies. Markdown for README.

## Global Constraints

- Go 1.25.11+; module path `github.com/jpvelasco/fabrica`.
- README edits are SURGICAL — do not rewrite or restructure already-current sections; match the existing `###`/`####` heading format and terse voice.
- Document ONLY flags/behavior that real `fabrica <cmd> --help` shows — never invent flags. The exact `--help` content for the new commands is embedded in Task 1 verbatim.
- The drift guard is `_test.go` (test code), no `//go:build` tag → runs in default `go test ./...` and CI. It must be MEANINGFUL (fails when a command is undocumented), not a no-op.
- The guard is ONE-DIRECTIONAL: command → documented-in-README. It does NOT assert README→command (fuzzy). `destroy --all` is a FLAG on `destroy`, not a subcommand — the guard checks that `destroy` is documented; the `--all` prose is a manual concern (encode this in a code comment).
- Skip cobra built-ins in the guard: `help`, `completion` (and their children).
- Naming: `snake_case.go` files; acronyms uppercase; `fmt.Print*` only.
- Quality gate: `go build ./... && go test ./... && go vet ./... && gofmt -l .` clean; `golangci-lint run ./...` 0 issues.

## Verified facts (from the code, 2026-07-04)

- README `## Current Status` (line ~19) has a stale prose line + a status table where `cost` = **Planned** and the `ci` row omits `destroy`; no `destroy --all` row.
- README Commands has no `### Cost` section; `### CI` stops at `ci logs`; `### Other` has only `version`.
- `root.New(out io.Writer) *cobra.Command` (`cmd/root/root.go:35`) returns the fully-wired tree; `cmd.Commands()` recurses it. Leaf commands include: `cost report`, `cost forecast`, `cost alerts list`, `cost alerts set`, `cost alerts check`, `ci destroy`, plus all already-documented ones. `destroy` is a runnable leaf (has `--all`). `config` exposes `config show`.
- Real `--help` (embedded verbatim in Task 1).

---

### Task 1: README accuracy patch

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: nothing (docs).
- Produces: a README where the status table + Commands section cover every shipped command. Task 2's guard depends on the command paths `cost report`, `cost forecast`, `cost alerts list`, `cost alerts set`, `cost alerts check`, `ci destroy`, `destroy` all appearing in README.md.

- [ ] **Step 1: Fix the Current Status prose + table**

In `README.md`, replace the stale status block. Find:

```markdown
**Phase 0 complete. Perforce, Horde, and Workstation modules are implemented.**
See [ROADMAP.md](ROADMAP.md) for phases, the Praetorium vision, and what's next.
```

Replace with:

```markdown
**Phase 0 complete; Phase 1 core complete.** All provisioning modules (Perforce,
Horde, Workstation), CI, Deploy, and Cost ship today, along with full-stack
`destroy --all` teardown and a CLI end-to-end test suite.
See [ROADMAP.md](ROADMAP.md) for phases, the Praetorium vision, and what's next.
```

Then in the status table just below, change the `cost` row from `Planned` to `Complete` and add a `destroy --all` row. Find:

```markdown
| `ci` | `setup`, `trigger`, `status`, `logs` | Complete |
| `deploy` | `setup`, `promote`, `rollback`, `status`, `destroy` | Complete |
| `cost` | `report`, `forecast`, `alerts` | Planned |
```

Replace with:

```markdown
| `ci` | `setup`, `trigger`, `status`, `logs`, `destroy` | Complete |
| `deploy` | `setup`, `promote`, `rollback`, `status`, `destroy` | Complete |
| `cost` | `report`, `forecast`, `alerts` | Complete |
| `destroy --all` | full-stack teardown | Complete |
```

- [ ] **Step 2: Add `#### fabrica ci destroy` to the CI section**

In `README.md`, find the `#### fabrica ci logs <build-id>` subsection:

```markdown
#### `fabrica ci logs <build-id>`

Fetches the CloudWatch log output for a specific build.
```

Insert AFTER it (before the `**Example pipeline:**` block):

```markdown
#### `fabrica ci destroy`

Tears down the CI infrastructure: deletes the CodeBuild project (via the AWS SDK), then the IAM service role (via Cloud Control). A missing project is not an error. Typed-phrase confirmation before any deletion; `--yes` to skip, `--dry-run` to preview.
```

- [ ] **Step 3: Add the `### Cost` section**

In `README.md`, the `### Other` section currently holds only `version`. Add a new `### Cost` section immediately BEFORE `### Other`. Find:

```markdown
### Other

#### `fabrica version`
```

Insert BEFORE it:

```markdown
### Cost

> **Offline cost visibility:** `fabrica cost` derives estimated monthly cost from your current `fabrica.yaml`, scoped to the modules present in local state. Fully offline — no AWS Cost Explorer calls, no billing API. Estimates reflect config, so run `<module> status` to reconcile if config changed since provisioning.

#### `fabrica cost report`

Shows the estimated monthly cost broken down by provisioned module and resource, with a grand total and confidence level. Reads local state (which modules exist) + `fabrica.yaml` (their cost inputs). `--json` for machine-readable output.

#### `fabrica cost forecast`

Projects the current monthly estimate over a time horizon: daily burn rate, total over the horizon, and annualized cost. `--days <n>` sets the horizon (default 30). `--json` for machine-readable output.

#### `fabrica cost alerts`

Manages local budget thresholds (written to `fabrica.yaml` — no AWS Budgets resources are created) and checks the current estimate against them:

- `fabrica cost alerts list` — show configured thresholds.
- `fabrica cost alerts set <scope> <monthly> [--warn-pct N]` — upsert a threshold (`scope` is `total` or a module name; `--warn-pct` defaults to 80). Honors `--dry-run`.
- `fabrica cost alerts check` — evaluate the current estimate against thresholds and report OK/WARN/OVER. Informational (exit code stays 0). `--json` for machine-readable output.

```

- [ ] **Step 4: Add `#### fabrica destroy --all` under `### Other`**

In `README.md`, find the `### Other` section:

```markdown
### Other

#### `fabrica version`

Prints version, commit hash, Go toolchain version, and platform.
```

Insert `destroy --all` BEFORE `#### fabrica version` (so `### Other` now has both):

```markdown
#### `fabrica destroy --all`

Full-stack teardown: destroys every provisioned module in reverse dependency order (deploy → ci → workstation → horde → perforce), then the state backend — but only if every module succeeded (a module failure preserves the backend so orphaned resources stay tracked for retry). One aggregate typed-phrase confirmation; `--yes` to skip, `--dry-run` to preview the full plan. Plain `fabrica destroy` (no `--all`) just prints usage.

```

- [ ] **Step 5: Verify every documented command against `--help`**

Build and spot-check that the README's command names + flags match reality. Run:

```bash
go build -o /tmp/fab . && for c in "cost report" "cost forecast" "cost alerts set" "ci destroy" "destroy"; do echo "== $c =="; /tmp/fab $c --help 2>&1 | grep -A6 "Flags:"; done
```

Expected: `cost forecast` shows `--days` (default 30); `cost alerts set` shows `--warn-pct`; `destroy` shows `-a, --all`; `ci destroy` has only `--help` (global `--yes`/`--dry-run` apply). Confirm the README text you added names exactly these flags and no invented ones. Fix any mismatch in the README.

- [ ] **Step 6: Commit**

```bash
git add README.md
git commit -m "docs: README — document cost, ci destroy, destroy --all; correct status (Milestone 5)"
```

---

### Task 2: Doc-drift guard test + ROADMAP update

**Files:**
- Create: `cmd/root/docs_drift_test.go`
- Modify: `ROADMAP.md`

**Interfaces:**
- Consumes: `root.New` (from `cmd/root`); `README.md` (read at test time).
- Produces: a `package root_test` test `TestEveryCommandIsDocumented` that fails listing any runnable leaf command whose path is absent from README.md.

- [ ] **Step 1: Write the guard test**

Create `cmd/root/docs_drift_test.go`. It walks the command tree from `root.New(io.Discard)`, collects runnable leaf command paths (space-joined, without the `fabrica` root name), skips cobra built-ins, and asserts each path is a substring of `README.md`. README is at the repo root — two levels up from `cmd/root/`.

```go
package root_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/root"
	"github.com/spf13/cobra"
)

// builtins are cobra-generated commands that are not part of Fabrica's
// documented surface.
var builtins = map[string]bool{"help": true, "completion": true}

// leafCommandPaths walks the command tree and returns the space-joined path of
// every runnable leaf command (a command with no subcommands that has a RunE),
// relative to the root (the root's own name is dropped). Built-in subtrees are
// skipped. destroy --all is NOT a subcommand — it is a flag on the runnable
// leaf `destroy`, so it is covered by asserting `destroy` is documented; the
// `--all` prose itself is verified manually, not by this guard.
func leafCommandPaths(c *cobra.Command, prefix []string) []string {
	if builtins[c.Name()] {
		return nil
	}
	// The root command itself carries no path segment.
	var here []string
	if len(prefix) == 0 && c.Parent() == nil {
		here = nil
	} else {
		here = append(append([]string{}, prefix...), c.Name())
	}

	children := c.Commands()
	var out []string
	hasRunnableChild := false
	for _, sub := range children {
		if builtins[sub.Name()] {
			continue
		}
		hasRunnableChild = true
		out = append(out, leafCommandPaths(sub, here)...)
	}
	// A leaf = no (non-builtin) children AND runnable.
	if !hasRunnableChild && c.Runnable() && len(here) > 0 {
		out = append(out, strings.Join(here, " "))
	}
	return out
}

func TestEveryCommandIsDocumented(t *testing.T) {
	rootCmd := root.New(io.Discard)

	// README lives at the repo root, two dirs up from cmd/root/.
	readmePath := filepath.Join("..", "..", "README.md")
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("reading README (%s): %v", readmePath, err)
	}
	readme := string(data)

	paths := leafCommandPaths(rootCmd, nil)
	if len(paths) == 0 {
		t.Fatal("no leaf commands found — the tree walk is broken")
	}

	var missing []string
	for _, p := range paths {
		if !strings.Contains(readme, p) {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		t.Errorf("these commands are not documented in README.md (add them, or the doc-drift guard fails):\n  %s",
			strings.Join(missing, "\n  "))
	}
}
```

- [ ] **Step 2: Run the guard — it must PASS (Task 1 documented everything)**

Run: `go test ./cmd/root/ -run TestEveryCommandIsDocumented -v`
Expected: PASS. If it lists missing commands, Task 1 left a gap — add the missing command(s) to README.md and re-run. (This is the guard doing its job.)

Note: the walk drops the root name, so paths look like `cost report`, `ci destroy`, `perforce create`, `destroy`, `version`, `config show`. Each must appear verbatim in README (Task 1 ensures the new ones do; the existing ones already appear in their `#### fabrica <path>` headings — the substring `cost report` is inside `` `fabrica cost report` ``).

- [ ] **Step 3: Verify the guard actually catches drift (negative check, then revert)**

Temporarily prove the guard fails when a command is undocumented:

```bash
# Remove the cost report line from a COPY of README, point the test at it — or
# simplest: temporarily delete the "cost report" mention and confirm failure.
cp README.md /tmp/README.bak
sed -i 's/fabrica cost report/fabrica cost XXXX/' README.md
go test ./cmd/root/ -run TestEveryCommandIsDocumented 2>&1 | grep -q "cost report" && echo "GUARD WORKS: caught the missing command" || echo "GUARD BROKEN: did not catch it"
cp /tmp/README.bak README.md   # revert
go test ./cmd/root/ -run TestEveryCommandIsDocumented 2>&1 | tail -1  # confirm green again
```

Expected: "GUARD WORKS" printed, then the final run PASSES after revert. Confirm `git diff README.md` is empty after this step (the revert restored it exactly).

- [ ] **Step 4: Update ROADMAP.md**

In `ROADMAP.md`, under Milestone 5, check the documentation line. Find:

```markdown
- ⬜ Comprehensive documentation and examples
```

Replace with:

```markdown
- ✅ README refresh (full command coverage) + doc-drift CI guard
```

(Leave the "Final architecture + consistency review" and "release preparation" lines unchecked.)

- [ ] **Step 5: Full gate**

Run:
```bash
go build ./... && go test ./... && go vet ./... && gofmt -l .
golangci-lint run ./...
```
Expected: build clean, ALL tests pass (incl. the new guard), vet clean, `gofmt -l` prints nothing, golangci-lint 0 issues.

- [ ] **Step 6: Commit**

```bash
git add cmd/root/docs_drift_test.go ROADMAP.md
git commit -m "test: doc-drift guard — every CLI command must be documented in README (Milestone 5)"
```

## Plan complete

Spec coverage:
- README Current Status prose + table (cost→Complete, ci+destroy, destroy --all row) → Task 1 Steps 1.
- Missing command sections (cost family, ci destroy, destroy --all) → Task 1 Steps 2-4.
- Verify-against-`--help` correctness pass → Task 1 Step 5.
- Doc-drift guard (walk tree, one-directional, skip builtins, destroy --all is a flag) → Task 2 Steps 1-3.
- ROADMAP M5 docs line → Task 2 Step 4.
- Out-of-scope (per-module guides, rewrite, README→CLI machine check, CLAUDE.md) untouched.
