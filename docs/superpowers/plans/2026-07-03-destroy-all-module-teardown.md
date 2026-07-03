# `destroy --all` Module Teardown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `fabrica destroy --all` tear down every provisioned module (deploy, ci, workstation, horde, perforce) in reverse dependency order, then the state backend — deleting the backend only if every module succeeded.

**Architecture:** A new `cmd/internal/destroyall` orchestration engine drives per-module teardown by reusing the existing `teardown.Command` engine (extended with a `SkipConfirm` seam so it doesn't re-prompt per module). CI, whose CodeBuild project is not a Cloud Control resource, gets a purpose-built `ci destroy` subcommand (SDK project delete + Cloud Control role delete) that the orchestrator reuses. `cmd/destroy` delegates to the orchestrator when `--all` is set.

**Tech Stack:** Go 1.25.11, Cobra, AWS Cloud Control + CodeBuild SDK (via `cloud.CodeBuildRunner`), `fmt.Print*` for output. Standard `testing`.

## Global Constraints

- Go 1.25.11+; module path `github.com/jpvelasco/fabrica`.
- Teardown order is fixed: **deploy → ci → workstation → horde → perforce → [state backend]**.
- Backend (S3 + DynamoDB) is deleted **only if every module teardown succeeded**. Any module failure ⇒ backend preserved.
- On a module failure, the orchestrator **continues** to the remaining modules, collects errors, and returns a non-nil error at the end.
- Single aggregate confirmation phrase: `destroy all <account>` via `prompt.ConfirmExact`. `--yes` skips it. `--dry-run` shows the full plan and makes no AWS calls.
- Deploy is torn down with deploy-level `all=true` (removes alias + IAM role too) — a full wipe leaves nothing behind.
- `teardown.Command.SkipConfirm` defaults false; every existing caller (perforce/horde/workstation/deploy destroy) is unchanged.
- Naming: `snake_case.go` files; `New*` returns pointers; single-letter receivers; acronyms uppercase (`ID`, `ARN`, `URL`, `AWS`, `IAM`).
- `fmt.Printf`/`Println` only — no logging library.
- Two-file test pattern for new command packages: white-box `*_test.go` (call `run()`/`Run()` with injected seams) + black-box `cobra_test.go` (minimal root replicating `--dry-run`/`--yes`/`--json` persistent flags).
- Coverage target: 60%+ for new `cmd/*` and `cmd/internal/*` code.

---

## File Structure

**New files:**
- `cmd/ci/destroy/destroy.go` — `ci destroy` subcommand: delete CodeBuild project (SDK) + IAM role (Cloud Control), state-persist after each; `skipConfirm` field for orchestrated use.
- `cmd/ci/destroy/destroy_test.go` — white-box tests (fake CodeBuildRunner + fake delete + state seams).
- `cmd/ci/destroy/cobra_test.go` — black-box wiring test.
- `cmd/internal/destroyall/destroyall.go` — orchestration engine: plan build, aggregate confirm, ordered module teardown, failure handling, backend delete, text + JSON output.
- `cmd/internal/destroyall/destroyall_test.go` — white-box: order, dry-run, confirm accept/reject, one-fails-skips-backend, all-succeed-deletes-backend, empty state, JSON.

**Modified files:**
- `cmd/internal/teardown/teardown.go` — add `SkipConfirm bool`; honor it in `Run` (skip confirm + standalone plan/confirm output when true). No behavior change when false.
- `cmd/ci/ci.go` — wire the new `destroy` subcommand.
- `cmd/destroy/destroy.go` — when `--all`, delegate to `destroyall.Run`; keep the plain-`destroy` usage hint. The existing backend-only path is invoked by the orchestrator (one code path for the backend).

**Build order:** `teardown.SkipConfirm` (Task 1) → `ci destroy` (Task 2) → `ci` parent wiring (folded into Task 2) → `destroyall` engine (Task 3) → `cmd/destroy` delegation (Task 4) → docs (Task 5). Each task compiles and tests green before the next.

---

### Task 1: Add `SkipConfirm` to `teardown.Command`

**Files:**
- Modify: `cmd/internal/teardown/teardown.go`
- Test: `cmd/internal/teardown/teardown_test.go`

**Interfaces:**
- Consumes: existing `teardown.Command`, `teardown.Spec`.
- Produces: `teardown.Command.SkipConfirm bool` — when true, `Run` skips the interactive confirmation AND the standalone plan/confirm output, proceeding straight to `apply` (after the not-provisioned and dry-run checks). Default false = unchanged behavior.

- [ ] **Step 1: Write the failing test**

Add to `cmd/internal/teardown/teardown_test.go`. This test builds a `Command` with `SkipConfirm: true`, a `Confirm` seam that fails the test if called, and fake state + delete seams; it asserts the resource is deleted without the confirm seam firing.

```go
func TestRunSkipConfirmBypassesConfirmation(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-1"},
	})

	var deleted []string
	var written bool
	c := Command{
		Spec:      Spec{ModuleName: "perforce", Verb: "destroy", Title: "Perforce", SuccessMessage: "done"},
		Runtime:   globals.Runtime{},
		SkipConfirm: true,
		Out:       &bytes.Buffer{},
		Confirm: func(string, string) bool {
			t.Fatal("Confirm must not be called when SkipConfirm is true")
			return false
		},
		ReadState:  func() (*fabricastate.State, error) { return st, nil },
		WriteState: func(*fabricastate.State) error { written = true; return nil },
		DeleteResource: func(_ context.Context, r *cloud.Resource) error {
			deleted = append(deleted, r.Identifier)
			return nil
		},
	}
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "sg-1" {
		t.Fatalf("expected sg-1 deleted, got %v", deleted)
	}
	if !written {
		t.Fatal("expected state to be written")
	}
}
```

Ensure the test file imports `bytes`, `context`, `github.com/jpvelasco/fabrica/cmd/globals`, `github.com/jpvelasco/fabrica/internal/cloud`, and `fabricastate "github.com/jpvelasco/fabrica/internal/state"` (some may already be present).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/internal/teardown/ -run TestRunSkipConfirmBypassesConfirmation`
Expected: FAIL — `SkipConfirm` is not a field of `Command` (compile error).

- [ ] **Step 3: Add the field**

In `cmd/internal/teardown/teardown.go`, add the field to the `Command` struct (after `Out io.Writer`):

```go
	// SkipConfirm, when true, bypasses the interactive confirmation and the
	// standalone plan/confirmation output — used when an orchestrator (destroy
	// --all) has already confirmed the aggregate operation.
	SkipConfirm bool
```

- [ ] **Step 4: Honor it in `Run`**

In `Run`, replace the confirmation block. The current code is:

```go
	if !c.JSONOut {
		c.printPlan(m, resources)
	}

	if !c.AssumeYes {
		fmt.Fprintln(c.Out)
		phrase := c.confirmPhrase(account)
		c.printConfirmInstructions(phrase)
		if !c.Confirm("Enter confirmation phrase", phrase) {
			fmt.Fprintln(c.Out, "Cancelled. No AWS calls were made.")
			return nil
		}
		fmt.Fprintln(c.Out, "Confirmation accepted.")
	} else if !c.JSONOut {
		fmt.Fprintln(c.Out)
		fmt.Fprintln(c.Out, "Proceeding without interactive confirmation (--yes flag set).")
	}

	return c.apply(ctx, st, m, resources)
```

Wrap the plan-print and confirmation in a `if !c.SkipConfirm` guard so the orchestrated path skips both:

```go
	if !c.SkipConfirm {
		if !c.JSONOut {
			c.printPlan(m, resources)
		}

		if !c.AssumeYes {
			fmt.Fprintln(c.Out)
			phrase := c.confirmPhrase(account)
			c.printConfirmInstructions(phrase)
			if !c.Confirm("Enter confirmation phrase", phrase) {
				fmt.Fprintln(c.Out, "Cancelled. No AWS calls were made.")
				return nil
			}
			fmt.Fprintln(c.Out, "Confirmation accepted.")
		} else if !c.JSONOut {
			fmt.Fprintln(c.Out)
			fmt.Fprintln(c.Out, "Proceeding without interactive confirmation (--yes flag set).")
		}
	}

	return c.apply(ctx, st, m, resources)
```

Note: `account := c.resolveAccount(st)` is computed just above this block. It is only used to build the confirm phrase, which now only runs inside the guard. To avoid an "unused variable" compile error when the guard is false, move the `account := c.resolveAccount(st)` line INSIDE the `if !c.SkipConfirm && !c.AssumeYes` scope, i.e. compute it right before `phrase := c.confirmPhrase(account)`:

```go
	if !c.SkipConfirm {
		if !c.JSONOut {
			c.printPlan(m, resources)
		}

		if !c.AssumeYes {
			account := c.resolveAccount(st)
			fmt.Fprintln(c.Out)
			phrase := c.confirmPhrase(account)
			c.printConfirmInstructions(phrase)
			if !c.Confirm("Enter confirmation phrase", phrase) {
				fmt.Fprintln(c.Out, "Cancelled. No AWS calls were made.")
				return nil
			}
			fmt.Fprintln(c.Out, "Confirmation accepted.")
		} else if !c.JSONOut {
			fmt.Fprintln(c.Out)
			fmt.Fprintln(c.Out, "Proceeding without interactive confirmation (--yes flag set).")
		}
	}

	return c.apply(ctx, st, m, resources)
```

Delete the now-orphaned `account := c.resolveAccount(st)` line that previously sat above the plan print (search for it between the `printDryRun` return and the plan block). `resolveAccount` remains used (inside the guard), so no dead code.

- [ ] **Step 5: Run tests**

Run: `go test ./cmd/internal/teardown/`
Expected: PASS (new test + all existing teardown tests — confirms default behavior unchanged). Then `go build ./...` — clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/internal/teardown/teardown.go cmd/internal/teardown/teardown_test.go
git commit -m "feat(teardown): SkipConfirm seam for orchestrated teardown"
```

---

### Task 2: `ci destroy` subcommand

**Files:**
- Create: `cmd/ci/destroy/destroy.go`
- Test: `cmd/ci/destroy/destroy_test.go`, `cmd/ci/destroy/cobra_test.go`
- Modify: `cmd/ci/ci.go` (wire the subcommand)

**Interfaces:**
- Consumes: `globals.{Runtime,RuntimeSource,OptionsSource}`, `provision.ReadState`, `ci.{TypeAWSCodeBuildProject,TypeAWSIAMRole}`, `cloud.CodeBuildRunner.DeleteProject`, `cloud.ResourceClient.Delete`, `prompt.ConfirmExact`, `fabricastate.WriteState`.
- Produces: `destroy.New(runtimeSource, optionsSource, out) *cobra.Command`. The `command` struct carries a `skipConfirm bool` field and a `run(ctx) error` method. Deletion order: CodeBuild project (SDK) → IAM role (Cloud Control). State persisted after each; module removed at end.

- [ ] **Step 1: Write the failing white-box test**

Create `cmd/ci/destroy/destroy_test.go`:

```go
package destroy

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func seededCIState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild"},
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})
	return st
}

func TestRunDeletesProjectThenRole(t *testing.T) {
	st := seededCIState()
	var deletedProject string
	var deletedResources []string
	c := command{
		runtime:     globals.Runtime{},
		out:         &bytes.Buffer{},
		skipConfirm: true,
		readState:   func() (*fabricastate.State, error) { return st, nil },
		writeState:  func(*fabricastate.State) error { return nil },
		deleteProject: func(_ context.Context, name string) error {
			deletedProject = name
			return nil
		},
		deleteResource: func(_ context.Context, r *cloud.Resource) error {
			deletedResources = append(deletedResources, r.Identifier)
			return nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if deletedProject != "fabrica-ci" {
		t.Fatalf("project delete = %q, want fabrica-ci", deletedProject)
	}
	if len(deletedResources) != 1 || deletedResources[0] != "fabrica-ci-codebuild" {
		t.Fatalf("role delete = %v, want [fabrica-ci-codebuild]", deletedResources)
	}
	if st.GetModule("ci") != nil {
		t.Fatal("ci module should be removed from state after teardown")
	}
}

func TestRunNotProvisioned(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	var out bytes.Buffer
	c := command{
		runtime:     globals.Runtime{},
		out:         &out,
		skipConfirm: true,
		readState:   func() (*fabricastate.State, error) { return st, nil },
		writeState:  func(*fabricastate.State) error { return nil },
		deleteProject:  func(context.Context, string) error { t.Fatal("no delete expected"); return nil },
		deleteResource: func(context.Context, *cloud.Resource) error { t.Fatal("no delete expected"); return nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("not provisioned")) {
		t.Fatalf("expected not-provisioned message, got:\n%s", out.String())
	}
}

func TestRunProjectMissingIsNotError(t *testing.T) {
	st := seededCIState()
	c := command{
		runtime:     globals.Runtime{},
		out:         &bytes.Buffer{},
		skipConfirm: true,
		readState:   func() (*fabricastate.State, error) { return st, nil },
		writeState:  func(*fabricastate.State) error { return nil },
		// DeleteProject swallows missing-project per the CodeBuildRunner contract,
		// so a nil return here models that; run() must not error.
		deleteProject:  func(context.Context, string) error { return nil },
		deleteResource: func(context.Context, *cloud.Resource) error { return nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run should tolerate missing project: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/ci/destroy/`
Expected: FAIL — package/`command` undefined.

- [ ] **Step 3: Implement `cmd/ci/destroy/destroy.go`**

```go
// Package destroy implements "fabrica ci destroy": tear down the CI
// infrastructure — the CodeBuild project (via the CodeBuildRunner SDK, since
// AWS::CodeBuild::Project has no Cloud Control CREATE/DELETE) and the IAM role
// (via Cloud Control). State is persisted after each deletion so a partial
// failure is recoverable.
package destroy

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName = "ci"
	lineWidth  = 58
)

type command struct {
	runtime     globals.Runtime
	dryRun      bool
	assumeYes   bool
	skipConfirm bool
	jsonOut     bool
	out         io.Writer

	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	deleteProject  func(ctx context.Context, name string) error
	deleteResource func(ctx context.Context, r *cloud.Resource) error
	confirm        func(string, string) bool
}

// New returns the "ci destroy" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Tear down the CI infrastructure (CodeBuild project + IAM role)",
		Long: `Delete the Fabrica CI resources: the CodeBuild project and its IAM service role.

Deletion order is project, then role. A missing project is not an error. You are
asked to type a confirmation phrase before any deletion; pass --yes to skip, or
--dry-run to preview.`,
		Example: `  fabrica ci destroy --dry-run
  fabrica ci destroy
  fabrica ci destroy --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:    rt,
				dryRun:     opts.DryRun,
				assumeYes:  opts.AssumeYes,
				jsonOut:    opts.JSONOutput,
				out:        out,
				readState:  func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				writeState: fabricastate.WriteState,
				confirm:    prompt.ConfirmExact,
			}
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					c.deleteResource = rc.Delete
				}
				if r, ok := rt.Provider.(cloud.CodeBuildRunner); ok {
					c.deleteProject = r.DeleteProject
				}
			}
			return c.run(cmd.Context())
		},
	}
}

func (c command) run(ctx context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	m := st.GetModule(moduleName)
	if m == nil {
		fmt.Fprintln(c.out, "CI is not provisioned. Nothing to destroy.")
		return nil
	}

	project, hasProject := stateutil.ResourceByType(m, ci.TypeAWSCodeBuildProject)
	role, hasRole := stateutil.ResourceByType(m, ci.TypeAWSIAMRole)

	if c.dryRun {
		fmt.Fprintln(c.out, "CI (destroy dry run)")
		fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
		fmt.Fprintln(c.out, "Resources that would be deleted (in order):")
		if hasProject {
			fmt.Fprintf(c.out, "  1. %s: %s\n", ci.TypeAWSCodeBuildProject, project.Identifier)
		}
		if hasRole {
			fmt.Fprintf(c.out, "  2. %s: %s\n", ci.TypeAWSIAMRole, role.Identifier)
		}
		fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
		return nil
	}

	account := st.Account
	if c.runtime.Config != nil && c.runtime.Config.Cloud.AWS.AccountID != "" {
		account = c.runtime.Config.Cloud.AWS.AccountID
	}

	if !c.skipConfirm {
		phrase := fmt.Sprintf("destroy %s %s", moduleName, account)
		if !c.assumeYes {
			fmt.Fprintln(c.out, "CI — destroy plan")
			fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
			if hasProject {
				fmt.Fprintf(c.out, "  CodeBuild project: %s\n", project.Identifier)
			}
			if hasRole {
				fmt.Fprintf(c.out, "  IAM role:          %s\n", role.Identifier)
			}
			fmt.Fprintln(c.out, "IRREVERSIBLE: deletes the CodeBuild project and IAM role.")
			fmt.Fprintln(c.out)
			fmt.Fprintf(c.out, "Type this exact phrase to continue:\n\n  %s\n\n", phrase)
			if !c.confirm("Enter confirmation phrase", phrase) {
				fmt.Fprintln(c.out, "Cancelled. No AWS calls were made.")
				return nil
			}
			fmt.Fprintln(c.out, "Confirmation accepted.")
		} else {
			fmt.Fprintln(c.out, "Proceeding without interactive confirmation (--yes flag set).")
		}
	}

	return c.apply(ctx, st, m, project, hasProject, role, hasRole)
}

func (c command) apply(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, project fabricastate.ModuleResource, hasProject bool, role fabricastate.ModuleResource, hasRole bool) error {
	if hasProject {
		if c.deleteProject == nil {
			return fmt.Errorf("cloud provider does not support CodeBuild project deletion — only AWS is supported in V1")
		}
		fmt.Fprintf(c.out, "Deleting CodeBuild project %s...\n", project.Identifier)
		if err := c.deleteProject(ctx, project.Identifier); err != nil {
			return fmt.Errorf("deleting CodeBuild project %s: %w", project.Identifier, err)
		}
		fmt.Fprintf(c.out, "  Deleted: %s\n", project.Identifier)
		c.removeAndPersist(st, m, ci.TypeAWSCodeBuildProject)
	}

	if hasRole {
		if c.deleteResource == nil {
			return fmt.Errorf("no provider configured; run 'fabrica setup' first")
		}
		fmt.Fprintf(c.out, "Deleting IAM role %s...\n", role.Identifier)
		r := &cloud.Resource{TypeName: ci.TypeAWSIAMRole, Identifier: role.Identifier}
		if err := c.deleteResource(ctx, r); err != nil {
			return fmt.Errorf("deleting IAM role %s: %w", role.Identifier, err)
		}
		fmt.Fprintf(c.out, "  Deleted: %s\n", role.Identifier)
		c.removeAndPersist(st, m, ci.TypeAWSIAMRole)
	}

	removeModule(st, moduleName)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}
	fmt.Fprintln(c.out, "CI infrastructure destroyed.")
	return nil
}

func (c command) removeAndPersist(st *fabricastate.State, m *fabricastate.ModuleState, typeName string) {
	filtered := m.Resources[:0]
	for _, r := range m.Resources {
		if r.TypeName != typeName {
			filtered = append(filtered, r)
		}
	}
	m.Resources = filtered
	st.UpsertModule(moduleName, m.Version, "destroying", m.Resources)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}
}

func removeModule(st *fabricastate.State, name string) {
	filtered := st.Modules[:0]
	for _, m := range st.Modules {
		if m.Name != name {
			filtered = append(filtered, m)
		}
	}
	st.Modules = filtered
}
```

- [ ] **Step 4: Run the white-box tests**

Run: `go test ./cmd/ci/destroy/`
Expected: PASS.

- [ ] **Step 5: Wire into the ci parent**

In `cmd/ci/ci.go`, add the import `"github.com/jpvelasco/fabrica/cmd/ci/destroy"` and, after the `logs.New(...)` line, add:

```go
	cmd.AddCommand(destroy.New(runtimeSource, optionsSource, out))
```

- [ ] **Step 6: Write the black-box cobra test**

Create `cmd/ci/destroy/cobra_test.go`. It builds a minimal root with the `--dry-run`/`--yes`/`--json` persistent flags, wires `destroy.New` with a `RuntimeSource` returning a `globals.Runtime{Config: config.Defaults()}` (nil provider is fine for the not-provisioned/dry-run path), and asserts `ci destroy --dry-run` runs without error on empty state.

```go
package destroy_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci/destroy"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func TestCIDestroyWiring(t *testing.T) {
	t.Chdir(t.TempDir()) // isolate from any real .fabrica/state.json
	var opts globals.Options
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: config.Defaults()}, nil }
	optionsSource := func() globals.Options { return opts }
	root.AddCommand(destroy.New(src, optionsSource, &out))
	root.SetArgs([]string{"destroy", "--dry-run"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
}
```

- [ ] **Step 7: Run tests + build**

Run: `go test ./cmd/ci/...`
Expected: PASS. Then `go build ./...` — clean. Then `go run . ci destroy --help` prints the help.

- [ ] **Step 8: Commit**

```bash
git add cmd/ci/destroy/ cmd/ci/ci.go
git commit -m "feat(ci): ci destroy subcommand (CodeBuild project + IAM role teardown)"
```

---

### Task 3: `cmd/internal/destroyall` orchestration engine

**Files:**
- Create: `cmd/internal/destroyall/destroyall.go`
- Test: `cmd/internal/destroyall/destroyall_test.go`

**Interfaces:**
- Consumes: `globals.Runtime`, `fabricastate.State`, `cloud.StateBackendDestroyer`, and a per-module teardown function seam.
- Produces:
  - `destroyall.ModuleTeardown` — a seam: `func(ctx context.Context) ([]string, error)` returning deleted IDs or an error, one per module.
  - `destroyall.Engine` struct with seam fields and a `Run(ctx) error` method.
  - `destroyall.Result` / `ModuleResult` JSON types (as in the spec).

Design note: to keep the orchestrator testable and free of import cycles, it does NOT import the module `destroy` packages directly. Instead the caller (`cmd/destroy`, Task 4) supplies an ordered slice of named teardown closures. The engine owns ordering-of-execution, failure aggregation, the aggregate confirm, and the backend gate — not the per-module wiring.

- [ ] **Step 1: Write the failing test**

Create `cmd/internal/destroyall/destroyall_test.go`:

```go
package destroyall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

func okTeardown(ids ...string) ModuleTeardown {
	return func(context.Context) ([]string, error) { return ids, nil }
}

func failTeardown(msg string) ModuleTeardown {
	return func(context.Context) ([]string, error) { return nil, errors.New(msg) }
}

type fakeBackend struct {
	bucketDeleted, tableDeleted bool
}

func (f *fakeBackend) DeleteStateBucket(_ context.Context, b string) (cloud.StateBackendDeleteResult, error) {
	f.bucketDeleted = true
	return cloud.StateBackendDeleteResult{Identifier: b, Deleted: true}, nil
}
func (f *fakeBackend) DeleteStateLockTable(_ context.Context, t string) (cloud.StateBackendDeleteResult, error) {
	f.tableDeleted = true
	return cloud.StateBackendDeleteResult{Identifier: t, Deleted: true}, nil
}

func baseEngine(out *bytes.Buffer, be *fakeBackend, mods []Module) Engine {
	return Engine{
		Account:   "123456789012",
		Region:    "us-east-1",
		Bucket:    "fabrica-state-123456789012",
		Table:     "fabrica-state-lock",
		Modules:   mods,
		Backend:   be,
		Out:       out,
		AssumeYes: true, // skip interactive confirm in most tests
		Confirm:   func(string, string) bool { return true },
	}
}

func TestRunAllSucceedDeletesBackend(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	e := baseEngine(&out, be, []Module{
		{Name: "deploy", Teardown: okTeardown("fleet-1")},
		{Name: "perforce", Teardown: okTeardown("i-1", "sg-1")},
	})
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !be.bucketDeleted || !be.tableDeleted {
		t.Fatal("backend should be deleted when all modules succeed")
	}
}

func TestRunModuleFailureSkipsBackend(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	e := baseEngine(&out, be, []Module{
		{Name: "deploy", Teardown: failTeardown("fleet stuck")},
		{Name: "perforce", Teardown: okTeardown("i-1")},
	})
	err := e.Run(context.Background())
	if err == nil {
		t.Fatal("expected an error when a module fails")
	}
	if be.bucketDeleted || be.tableDeleted {
		t.Fatal("backend MUST NOT be deleted when any module fails")
	}
	// remaining module still attempted
	if !strings.Contains(out.String(), "perforce") {
		t.Fatalf("expected perforce still torn down after deploy failure:\n%s", out.String())
	}
	// the failed module is named explicitly in the summary, with its error
	if !strings.Contains(out.String(), "deploy") || !strings.Contains(out.String(), "fleet stuck") {
		t.Fatalf("failure summary must name the failed module and its error:\n%s", out.String())
	}
	// the returned error also lists the failed module
	if !strings.Contains(err.Error(), "deploy") {
		t.Fatalf("returned error must name the failed module, got: %v", err)
	}
}

func TestRunExecutesInGivenOrder(t *testing.T) {
	var out bytes.Buffer
	var order []string
	track := func(name string) ModuleTeardown {
		return func(context.Context) ([]string, error) { order = append(order, name); return nil, nil }
	}
	e := baseEngine(&out, &fakeBackend{}, []Module{
		{Name: "deploy", Teardown: track("deploy")},
		{Name: "ci", Teardown: track("ci")},
		{Name: "perforce", Teardown: track("perforce")},
	})
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"deploy", "ci", "perforce"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("teardown order = %v, want %v", order, want)
	}
}

func TestRunDryRunNoDeletes(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	called := false
	e := baseEngine(&out, be, []Module{
		{Name: "deploy", Teardown: func(context.Context) ([]string, error) { called = true; return nil, nil }},
	})
	e.DryRun = true
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Fatal("dry-run must not invoke module teardown")
	}
	if be.bucketDeleted || be.tableDeleted {
		t.Fatal("dry-run must not delete backend")
	}
	if !strings.Contains(out.String(), "deploy") {
		t.Fatalf("dry-run should list modules:\n%s", out.String())
	}
}

func TestRunConfirmRejected(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	called := false
	e := baseEngine(&out, be, []Module{
		{Name: "deploy", Teardown: func(context.Context) ([]string, error) { called = true; return nil, nil }},
	})
	e.AssumeYes = false
	e.Confirm = func(string, string) bool { return false }
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called || be.bucketDeleted {
		t.Fatal("rejected confirmation must make no changes")
	}
	if !strings.Contains(out.String(), "Cancelled") {
		t.Fatalf("expected cancellation message:\n%s", out.String())
	}
}

func TestRunConfirmPhraseIsAggregate(t *testing.T) {
	var out bytes.Buffer
	var gotPhrase string
	e := baseEngine(&out, &fakeBackend{}, []Module{{Name: "deploy", Teardown: okTeardown()}})
	e.AssumeYes = false
	e.Confirm = func(_ string, phrase string) bool { gotPhrase = phrase; return true }
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotPhrase != "destroy all 123456789012" {
		t.Fatalf("phrase = %q, want %q", gotPhrase, "destroy all 123456789012")
	}
}

func TestRunEmptyNoModulesNoBackend(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	e := baseEngine(&out, be, nil)
	e.Bucket = ""
	e.Table = ""
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if be.bucketDeleted {
		t.Fatal("nothing to delete")
	}
}

func TestRunJSONOutput(t *testing.T) {
	var out bytes.Buffer
	e := baseEngine(&out, &fakeBackend{}, []Module{{Name: "deploy", Teardown: okTeardown("fleet-1")}})
	e.JSONOut = true
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var res Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if len(res.Modules) != 1 || res.Modules[0].Module != "deploy" || !res.BackendDeleted {
		t.Fatalf("unexpected result: %+v", res)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/internal/destroyall/`
Expected: FAIL — package/`Engine` undefined.

- [ ] **Step 3: Implement `cmd/internal/destroyall/destroyall.go`**

```go
// Package destroyall orchestrates a full-stack teardown for `fabrica destroy
// --all`: it runs a caller-supplied, ordered set of per-module teardown
// closures, then deletes the state backend — but only if every module
// succeeded. A module failure is collected (not fatal mid-run); remaining
// modules are still torn down, and the backend is preserved so orphaned
// resources stay tracked for a clean retry.
package destroyall

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

const lineWidth = 62

// ModuleTeardown tears down one module, returning the deleted resource IDs or
// an error. The caller (cmd/destroy) builds these closures over each module's
// teardown command with confirmation already skipped.
type ModuleTeardown func(ctx context.Context) ([]string, error)

// Module pairs a name with its teardown closure. Order in the slice is the
// execution order.
type Module struct {
	Name     string
	Teardown ModuleTeardown
}

// ModuleResult is the outcome of one module's teardown.
type ModuleResult struct {
	Module    string   `json:"module"`
	Destroyed []string `json:"destroyed"`
	Error     string   `json:"error,omitempty"`
}

// Result is the aggregate outcome of a destroy --all run.
type Result struct {
	Modules        []ModuleResult `json:"modules"`
	BackendDeleted bool           `json:"backendDeleted"`
	DryRun         bool           `json:"dryRun"`
}

// Engine runs the orchestrated teardown. Seam fields (Backend, Confirm) are
// wired to real implementations by cmd/destroy and replaced with fakes in tests.
type Engine struct {
	Account string
	Region  string
	Bucket  string
	Table   string
	Modules []Module

	Backend cloud.StateBackendDestroyer

	DryRun    bool
	AssumeYes bool
	JSONOut   bool
	Out       io.Writer

	Confirm func(msg, phrase string) bool
}

// Run executes the orchestrated teardown.
func (e Engine) Run(ctx context.Context) error {
	if len(e.Modules) == 0 && e.Bucket == "" && e.Table == "" {
		if e.JSONOut {
			e.printJSON(Result{Modules: []ModuleResult{}, DryRun: e.DryRun})
			return nil
		}
		fmt.Fprintln(e.Out, "No provisioned modules or state backend found. Nothing to destroy.")
		return nil
	}

	if e.DryRun {
		return e.runDryRun()
	}

	if !e.confirmAggregate() {
		return nil
	}

	return e.execute(ctx)
}

func (e Engine) runDryRun() error {
	if e.JSONOut {
		mods := make([]ModuleResult, 0, len(e.Modules))
		for _, m := range e.Modules {
			mods = append(mods, ModuleResult{Module: m.Name})
		}
		e.printJSON(Result{Modules: mods, DryRun: true})
		return nil
	}
	fmt.Fprintln(e.Out, "Destroy --all dry run")
	fmt.Fprintln(e.Out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(e.Out, "  Account: %s\n  Region:  %s\n\n", e.Account, e.Region)
	fmt.Fprintf(e.Out, "The following %d module(s) WOULD BE DELETED (in order):\n", len(e.Modules))
	if len(e.Modules) == 0 {
		fmt.Fprintln(e.Out, "  (none provisioned)")
	}
	for i, m := range e.Modules {
		fmt.Fprintf(e.Out, "  %d. %s (all its resources)\n", i+1, m.Name)
	}
	if e.Bucket != "" || e.Table != "" {
		fmt.Fprintln(e.Out, "\nThen the state backend WOULD BE DELETED:")
		fmt.Fprintf(e.Out, "  S3 bucket:      %s\n", e.Bucket)
		fmt.Fprintf(e.Out, "  DynamoDB table: %s\n", e.Table)
		fmt.Fprintln(e.Out, "  (backend is deleted only if every module above succeeds)")
	} else {
		fmt.Fprintln(e.Out, "\nNo state backend to delete.")
	}
	fmt.Fprintln(e.Out, "\nNothing has been deleted. Run without --dry-run to proceed.")
	return nil
}

func (e Engine) confirmAggregate() bool {
	if e.AssumeYes {
		if !e.JSONOut {
			fmt.Fprintln(e.Out, "Proceeding without interactive confirmation (--yes flag set).")
		}
		return true
	}
	phrase := fmt.Sprintf("destroy all %s", e.Account)
	fmt.Fprintln(e.Out, "Destroy --all plan")
	fmt.Fprintln(e.Out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(e.Out, "  Account: %s\n  Region:  %s\n\n", e.Account, e.Region)
	fmt.Fprintln(e.Out, "This will permanently delete ALL provisioned modules and the state backend.")
	fmt.Fprintln(e.Out, "Modules (in order):")
	for i, m := range e.Modules {
		fmt.Fprintf(e.Out, "  %d. %s\n", i+1, m.Name)
	}
	fmt.Fprintf(e.Out, "\nType this exact phrase to continue:\n\n  %s\n\n", phrase)
	if !e.Confirm("Enter confirmation phrase", phrase) {
		fmt.Fprintln(e.Out, "Cancelled. No AWS calls were made.")
		return false
	}
	fmt.Fprintln(e.Out, "Confirmation accepted.")
	return true
}

func (e Engine) execute(ctx context.Context) error {
	res := Result{Modules: make([]ModuleResult, 0, len(e.Modules))}
	anyFailed := false

	for _, m := range e.Modules {
		if !e.JSONOut {
			fmt.Fprintf(e.Out, "\n=== Tearing down %s ===\n", m.Name)
		}
		ids, err := m.Teardown(ctx)
		mr := ModuleResult{Module: m.Name, Destroyed: ids}
		if err != nil {
			mr.Error = err.Error()
			anyFailed = true
			if !e.JSONOut {
				fmt.Fprintf(e.Out, "  ERROR tearing down %s: %v\n", m.Name, err)
			}
		}
		res.Modules = append(res.Modules, mr)
	}

	if anyFailed {
		return e.finishWithFailure(res)
	}

	if err := e.deleteBackend(ctx, &res); err != nil {
		if e.JSONOut {
			e.printJSON(res)
		}
		return err
	}

	if e.JSONOut {
		e.printJSON(res)
		return nil
	}
	fmt.Fprintln(e.Out, "\nDestroy --all complete. All modules and the state backend were removed.")
	return nil
}

func (e Engine) finishWithFailure(res Result) error {
	failed := make([]string, 0)
	for _, m := range res.Modules {
		if m.Error != "" {
			failed = append(failed, m.Module)
		}
	}
	if e.JSONOut {
		e.printJSON(res)
	} else {
		fmt.Fprintln(e.Out, "\nDestroy --all did not complete: the following module(s) failed:")
		for _, m := range res.Modules {
			if m.Error != "" {
				fmt.Fprintf(e.Out, "  - %s: %s\n", m.Module, m.Error)
			}
		}
		fmt.Fprintln(e.Out, "The state backend was PRESERVED so orphaned resources stay tracked.")
		fmt.Fprintf(e.Out, "Retry the failed module(s) — e.g. 'fabrica %s destroy' — then re-run 'fabrica destroy --all'.\n", failed[0])
	}
	return fmt.Errorf("destroy --all incomplete: %d module(s) failed: %s", len(failed), strings.Join(failed, ", "))
}

func (e Engine) deleteBackend(ctx context.Context, res *Result) error {
	if e.Bucket == "" && e.Table == "" {
		return nil
	}
	if e.Backend == nil {
		return fmt.Errorf("provider does not support state backend destroy")
	}
	if !e.JSONOut {
		fmt.Fprintln(e.Out, "\n=== Deleting state backend ===")
	}
	if e.Bucket != "" {
		b, err := e.Backend.DeleteStateBucket(ctx, e.Bucket)
		if err != nil {
			e.printBackendFailure("S3 state bucket", e.Bucket, err)
			return fmt.Errorf("deleting state bucket %s: %w", e.Bucket, err)
		}
		e.printBackendResult("S3 state bucket", b)
	}
	if e.Table != "" {
		tbl, err := e.Backend.DeleteStateLockTable(ctx, e.Table)
		if err != nil {
			e.printBackendFailure("DynamoDB lock table", e.Table, err)
			return fmt.Errorf("deleting lock table %s: %w", e.Table, err)
		}
		e.printBackendResult("DynamoDB lock table", tbl)
	}
	res.BackendDeleted = true
	return nil
}

func (e Engine) printBackendResult(label string, r cloud.StateBackendDeleteResult) {
	if e.JSONOut {
		return
	}
	switch {
	case r.Deleted:
		fmt.Fprintf(e.Out, "  deleted %s: %s\n", label, r.Identifier)
	case r.Missing:
		fmt.Fprintf(e.Out, "  %s not found; skipping: %s\n", label, r.Identifier)
	default:
		fmt.Fprintf(e.Out, "  %s unchanged: %s\n", label, r.Identifier)
	}
}

func (e Engine) printBackendFailure(label, id string, err error) {
	if e.JSONOut {
		return
	}
	fmt.Fprintf(e.Out, "  failed to delete %s: %s\n", label, id)
	fmt.Fprintf(e.Out, "  Error: %v\n", err)
}

func (e Engine) printJSON(res Result) {
	data, _ := json.MarshalIndent(res, "", "  ")
	fmt.Fprintln(e.Out, string(data))
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/internal/destroyall/`
Expected: PASS (all cases). Then `go vet ./cmd/internal/destroyall/` — clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/internal/destroyall/
git commit -m "feat(destroyall): orchestration engine for destroy --all"
```

---

### Task 4: Wire `cmd/destroy --all` to the orchestrator

This task exposes an orchestrated-teardown constructor from each Cloud Control module `destroy` package, then wires `cmd/destroy --all` to build the ordered `[]destroyall.Module` and delegate to `destroyall.Engine`.

**Files:**
- Modify: `cmd/perforce/destroy/destroy.go`, `cmd/horde/destroy/destroy.go`, `cmd/workstation/terminate/terminate.go`, `cmd/deploy/destroy/destroy.go` — add an exported `NewTeardown(rt, out)` helper.
- Modify: `cmd/destroy/destroy.go` — delegate to `destroyall` when `--all`.
- Test: `cmd/destroy/destroy_test.go` — extend for the `--all` delegation.

**Interfaces:**
- Consumes: `destroyall.{Engine,Module,ModuleTeardown}`, `teardown.Command`, each module's `NewTeardown`, `cmd/ci/destroy`, `fabricastate.ResolveBackendNames`, `cloud.StateBackendDestroyer`.
- Produces: `<module>destroy.NewTeardown(rt globals.Runtime, out io.Writer) teardown.Command` from perforce/horde/workstation/deploy (SkipConfirm + AssumeYes preset by the orchestrator, provider seams wired). `cmd/destroy` builds the ordered module list and calls `destroyall.Engine.Run`.

- [ ] **Step 1: Add `NewTeardown` to each Cloud Control module destroy package**

Each module's `New()` RunE already wires a `teardown.Command`. Extract that wiring into an exported helper the orchestrator can call. The helper sets `SkipConfirm: true` and `AssumeYes: true` (the aggregate confirm already covered it) and wires provider seams.

**perforce** — in `cmd/perforce/destroy/destroy.go`, add (the package already imports teardown, globals, provision, cloud, fabricastate, prompt):

```go
// NewTeardown builds this module's teardown.Command for orchestrated use by
// `fabrica destroy --all`. Confirmation is skipped (the orchestrator confirms
// the aggregate operation).
func NewTeardown(rt globals.Runtime, out io.Writer) teardown.Command {
	tc := teardown.Command{
		Spec:        spec, // perforce uses a package-level `spec` var or spec builder — match what New() uses
		Runtime:     rt,
		SkipConfirm: true,
		AssumeYes:   true,
		Out:         out,
		Confirm:     prompt.ConfirmExact,
		ReadState:   func() (*fabricastate.State, error) { return provision.ReadState(rt) },
		WriteState:  fabricastate.WriteState,
	}
	if rt.Provider != nil {
		if rc := rt.Provider.Resources(); rc != nil {
			tc.DeleteResource = rc.Delete
			tc.GetResource = rc.Get
		}
	}
	return tc
}
```

IMPORTANT: match `Spec` to exactly how the package's `New()` builds it. Inspect the file first:
- If `New()` uses a package-level `var spec = teardown.Spec{...}` (workstation pattern), use `Spec: spec`.
- If `New()` uses a `spec()` / `spec(all)` function (perforce/horde/deploy pattern), call it the same way. **For deploy, use `spec(true)` and it already sets `ResourceOrder: resourceOrder(true)` — pass all=true so the alias + IAM role are removed** (full wipe).

**horde** — same as perforce, matching horde's `spec` construction.

**workstation** — in `cmd/workstation/terminate/terminate.go`, same helper but `Spec: spec` (workstation uses a package-level `spec` var). Name it `NewTeardown` for consistency. Note the module name is "workstation" (the package is `terminate`).

**deploy** — in `cmd/deploy/destroy/destroy.go`:

```go
// NewTeardown builds the deploy teardown.Command for orchestrated use by
// `fabrica destroy --all`, with all=true so the alias and IAM role are removed
// (a full-stack wipe leaves nothing behind).
func NewTeardown(rt globals.Runtime, out io.Writer) teardown.Command {
	tc := teardown.Command{
		Spec:        spec(true),
		Runtime:     rt,
		SkipConfirm: true,
		AssumeYes:   true,
		Out:         out,
		Confirm:     prompt.ConfirmExact,
		ReadState:   func() (*fabricastate.State, error) { return provision.ReadState(rt) },
		WriteState:  fabricastate.WriteState,
	}
	if rt.Provider != nil {
		if rc := rt.Provider.Resources(); rc != nil {
			tc.DeleteResource = rc.Delete
			tc.GetResource = rc.Get
		}
	}
	return tc
}
```

After adding each helper, run `go build ./...` — clean. These helpers are additive (nothing calls them yet), so existing tests are unaffected.

- [ ] **Step 2: Refactor each module's `New()` to reuse `NewTeardown` (DRY, optional but preferred)**

Where a module's `New()` RunE duplicates the exact seam-wiring now in `NewTeardown`, have `New()` build its `teardown.Command` by calling `NewTeardown(rt, out)` and then overriding the interactive fields (`SkipConfirm=false`, `AssumeYes=opts.AssumeYes`, `DryRun=opts.DryRun`, `JSONOut=opts.JSONOutput`). This keeps one wiring path. If a module's `New()` has extra pre-flight output (e.g. deploy's "alias will be preserved" note keys off `all`), leave that note logic in `New()`. Run `go test ./cmd/perforce/... ./cmd/horde/... ./cmd/workstation/... ./cmd/deploy/...` — all PASS (behavior unchanged).

- [ ] **Step 3: Write the failing test for `--all` delegation**

Add to `cmd/destroy/destroy_test.go`. The `command` struct (Task-4 modified, Step 4) gains a `runAll func(context.Context) error` seam so the test can assert delegation without real AWS. Test that `--all` (non-dry-run, confirmed) invokes the orchestrator seam, and that dry-run still prints a plan without invoking it:

```go
func TestDestroyAllDelegatesToOrchestrator(t *testing.T) {
	called := false
	c := command{
		runtime:   globals.Runtime{Provider: &fakeProvider{}, Config: config.Defaults()},
		all:       true,
		assumeYes: true,
		out:       &bytes.Buffer{},
		confirm:   func(string, string) bool { return true },
		runAll:    func(context.Context) error { called = true; return nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !called {
		t.Fatal("--all should delegate to the orchestrator seam")
	}
}
```

(Keep the existing backend-only dry-run/confirm tests; they now exercise the orchestrator's dry-run via the seam or remain asserting the plan text — adjust to the new structure as needed. If the existing tests construct `command` literally, add `runAll` to those literals or default it in a constructor.)

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./cmd/destroy/ -run TestDestroyAllDelegatesToOrchestrator`
Expected: FAIL — `runAll` is not a field of `command`.

- [ ] **Step 5: Rewire `cmd/destroy/destroy.go`**

Add a `runAll func(context.Context) error` seam to the `command` struct. In `New()`, wire it to a function that builds the ordered `[]destroyall.Module` and calls `destroyall.Engine.Run`. Replace the current backend-only `run` body for the `--all` case with a call to `c.runAll(ctx)`. Concretely:

1. Add imports: `destroyall "github.com/jpvelasco/fabrica/cmd/internal/destroyall"`, and the five module teardown packages plus `cidestroy "github.com/jpvelasco/fabrica/cmd/ci/destroy"`. To avoid a name clash (four packages are named `destroy`), alias them: `pfdestroy`, `hordedestroy`, `wsterminate`, `deploydestroy`, `cidestroy`.

2. Add the seam + build it in `New()`:

```go
c := command{
	runtime:   rt,
	all:       all,
	dryRun:    opts.DryRun,
	assumeYes: opts.AssumeYes,
	out:       out,
	confirm:   prompt.ConfirmExact,
}
c.runAll = func(ctx context.Context) error {
	return runAll(ctx, rt, opts, out)
}
return c.run(cmd.Context())
```

3. Add the package-level `runAll` builder (the one the seam calls). It resolves account/region, builds the ordered module list (only modules present in state), wires the CI teardown closure, resolves backend names, and runs the engine:

```go
func runAll(ctx context.Context, rt globals.Runtime, opts globals.Options, out io.Writer) error {
	if rt.Provider == nil {
		fmt.Fprintln(out, "No infrastructure found. Nothing to destroy.")
		return nil
	}
	account, _, region, err := rt.Provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("resolving identity: %w", err)
	}
	st, err := provision.ReadState(rt)
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	// Ordered candidate modules: deploy, ci, workstation, horde, perforce.
	// Include a module only if it is present in state.
	var mods []destroyall.Module
	add := func(name string, td destroyall.ModuleTeardown) {
		if st.GetModule(name) != nil {
			mods = append(mods, destroyall.Module{Name: name, Teardown: td})
		}
	}
	add("deploy", teardownClosure(ctx, deploydestroy.NewTeardown(rt, out)))
	add("ci", ciTeardownClosure(ctx, rt, out))
	add("workstation", teardownClosure(ctx, wsterminate.NewTeardown(rt, out)))
	add("horde", teardownClosure(ctx, hordedestroy.NewTeardown(rt, out)))
	add("perforce", teardownClosure(ctx, pfdestroy.NewTeardown(rt, out)))

	names := fabricastate.ResolveBackendNames(rt.Config, account)
	destroyer, _ := rt.Provider.(cloud.StateBackendDestroyer)

	e := destroyall.Engine{
		Account:   account,
		Region:    region,
		Bucket:    names.Bucket,
		Table:     names.Table,
		Modules:   mods,
		Backend:   destroyer,
		DryRun:    opts.DryRun,
		AssumeYes: opts.AssumeYes,
		JSONOut:   opts.JSONOutput,
		Out:       out,
		Confirm:   prompt.ConfirmExact,
	}
	return e.Run(ctx)
}

// teardownClosure adapts a teardown.Command into a destroyall.ModuleTeardown.
// teardown.Command.Run returns only an error and prints deleted IDs to Out, so
// the returned ID slice is nil (the per-module output already lists them).
func teardownClosure(_ context.Context, tc teardown.Command) destroyall.ModuleTeardown {
	return func(ctx context.Context) ([]string, error) {
		return nil, tc.Run(ctx)
	}
}

// ciTeardownClosure builds the CI teardown closure. CI is not a teardown.Command
// (its CodeBuild project is SDK-managed); it uses cmd/ci/destroy's orchestrated path.
func ciTeardownClosure(_ context.Context, rt globals.Runtime, out io.Writer) destroyall.ModuleTeardown {
	return func(ctx context.Context) ([]string, error) {
		return nil, cidestroy.RunOrchestrated(ctx, rt, out)
	}
}
```

4. In `run(ctx)`, when `c.all` is set, delegate:

```go
func (c command) run(ctx context.Context) error {
	if !c.all {
		c.printUsageHint()
		return nil
	}
	return c.runAll(ctx)
}
```

Remove the now-superseded backend-only logic from `run`/`destroyBackend` in `cmd/destroy` **only if** it is fully replaced by the orchestrator's `deleteBackend`. The engine now owns backend deletion, so the old `destroyBackend`, `printDestroyPlan`, `printDryRunPlan`, `confirmationPhrase`, etc. in `cmd/destroy` become dead code — delete them and their now-unused helpers. Keep `printUsageHint`. Verify with `go vet` (unused funcs won't fail vet, but `golangci-lint`'s `unused` will — run it in Step 7).

- [ ] **Step 6: Add `RunOrchestrated` to `cmd/ci/destroy`**

The CI closure needs a skip-confirm entry point. In `cmd/ci/destroy/destroy.go`, add:

```go
// RunOrchestrated runs the CI teardown with confirmation skipped, for use by
// `fabrica destroy --all`. The aggregate confirmation is handled by the orchestrator.
func RunOrchestrated(ctx context.Context, rt globals.Runtime, out io.Writer) error {
	c := command{
		runtime:     rt,
		skipConfirm: true,
		assumeYes:   true,
		out:         out,
		readState:   func() (*fabricastate.State, error) { return provision.ReadState(rt) },
		writeState:  fabricastate.WriteState,
		confirm:     prompt.ConfirmExact,
	}
	if rt.Provider != nil {
		if rc := rt.Provider.Resources(); rc != nil {
			c.deleteResource = rc.Delete
		}
		if r, ok := rt.Provider.(cloud.CodeBuildRunner); ok {
			c.deleteProject = r.DeleteProject
		}
	}
	return c.run(ctx)
}
```

- [ ] **Step 7: Run the full affected suite + lint**

Run: `go build ./... && go test ./cmd/... && go vet ./...`
Expected: build clean, tests pass, vet clean.
Run: `golangci-lint run ./cmd/...`
Expected: 0 issues (catches any dead code left from Step 5).
Run: `go run . destroy` (no flags) → prints usage hint. `go run . destroy --all --dry-run` → prints the aggregate plan (empty modules on a fresh checkout is fine).

- [ ] **Step 8: Commit**

```bash
git add cmd/destroy/ cmd/ci/destroy/ cmd/perforce/destroy/ cmd/horde/destroy/ cmd/workstation/terminate/ cmd/deploy/destroy/
git commit -m "feat(destroy): --all tears down all modules then backend via destroyall engine"
```

---

### Task 5: Docs — ROADMAP, CLAUDE.md, verification

**Files:**
- Modify: `ROADMAP.md`, `CLAUDE.md`
- No new tests (behavior covered by Tasks 1–4).

**Interfaces:**
- Consumes: the completed `destroy --all` + `ci destroy` behavior.
- Produces: accurate docs.

- [ ] **Step 1: Update `ROADMAP.md`**

1. Module-status table `destroy --all` row: change `⚠️ Skeleton wired` to:
   `✅ Complete — tears down all modules (deploy→ci→workstation→horde→perforce) then the state backend; backend deleted only on full success`.
2. `ci` row: append to its note that `ci destroy` now exists (setup/trigger/status/logs/**destroy**).
3. Under Milestone 5, check the "End-to-end testing + teardown" line's teardown portion as addressed for `destroy --all` (leave E2E testing itself unchecked — separate sub-project).

- [ ] **Step 2: Update `CLAUDE.md`**

1. Package tables — add/adjust rows:
   - `cmd/internal/destroyall` — orchestration engine for `destroy --all`: ordered per-module teardown (deploy→ci→workstation→horde→perforce) then state backend; backend deleted only if every module succeeds; aggregate confirmation phrase; text + JSON.
   - `cmd/ci/destroy` — `ci destroy`: deletes CodeBuild project (SDK `DeleteProject`) + IAM role (Cloud Control); `RunOrchestrated` entry point for `destroy --all`.
   - Update the `cmd/destroy` row: `destroy --all` now delegates to `destroyall` (full-stack teardown), plain `destroy` prints the usage hint.
2. In the `cmd/ci` command list, add `destroy` to the subcommands.
3. In the "Shared command helpers (`cmd/internal/*`)" section, add a bullet for `cmd/internal/destroyall` describing the orchestration + the backend-gate rule, and note the `teardown.SkipConfirm` seam it relies on.
4. In "CI-Specific Notes", update the out-of-scope line: `ci destroy` is now implemented (remove it from the V1 out-of-scope list, note the CodeBuild-SDK delete + Cloud Control role delete order).
5. Update the "Planned Command Structure" tree: add `fabrica ci ... destroy` and mark `fabrica destroy --all` as `✓ implemented; full-stack teardown`.
6. Add a "Destroy-Specific Notes" section (after "Deploy-Specific Notes") covering: teardown order + rationale, the backend-only-on-full-success gate, single aggregate confirm phrase (`destroy all <account>`), deploy torn down with `all=true`, CI's SDK-delete special case, and that a failure continues remaining modules and preserves the backend for retry.

- [ ] **Step 3: Full verification**

Run:
```bash
go build ./... && go test ./... && go vet ./... && gofmt -l .
```
Expected: build clean, ALL tests pass, vet clean, `gofmt -l` prints nothing.
Run: `golangci-lint run ./...`
Expected: 0 issues.
Run the dependency check: `go list -deps ./internal/cloud/... | grep -E 'internal/(state|cost)'`
Expected: prints nothing (cloud must not import state/cost).

- [ ] **Step 4: End-to-end smoke (offline, no AWS)**

```bash
go run . destroy                    # prints usage hint
go run . destroy --all --dry-run    # prints aggregate plan (modules from local state, if any, + backend)
go run . ci destroy --dry-run       # prints CI teardown plan or "not provisioned"
go run . ci destroy --help
go run . destroy --all --help
```
Expected: each behaves as described; no AWS calls on dry-run/help. Do NOT run `destroy --all` for real (it deletes infrastructure).

- [ ] **Step 5: Commit**

```bash
git add ROADMAP.md CLAUDE.md
git commit -m "docs: destroy --all full-stack teardown + ci destroy (Milestone 5)"
```

## Plan complete

Spec coverage:
- `SkipConfirm` seam on the teardown engine → Task 1.
- `ci destroy` (CodeBuild SDK + Cloud Control role; the CI special case) → Task 2.
- `destroyall` orchestration engine (order, aggregate confirm, failure-continues, backend-gate, dry-run, JSON) → Task 3.
- `destroy --all` delegation + per-module `NewTeardown` + deploy `all=true` + CI `RunOrchestrated` → Task 4.
- Docs/ROADMAP + verification + offline smoke → Task 5.
- Decisions #1–#4 (scope, CI included, single aggregate phrase, continue-on-failure + backend gate) are realized across Tasks 3–4 and documented in Task 5.
- Out-of-scope items (other M5 sub-projects, backup/restore, selective/parallel teardown) are untouched.
