# CLI E2E Test Harness — Design (Phase 1, Milestone 5, Sub-project B)

Status: approved for implementation
Date: 2026-07-03

## Goal

A fast, free, CI-runnable end-to-end test suite that drives real Fabrica
command flows against an in-memory fake cloud provider, asserting the full
triad — exit codes, output (human + `--json`), and `.fabrica/state.json` — across
realistic cross-command operator journeys.

**Constraint that shapes everything:** Fabrica is an open-source project with no
AWS budget. The E2E suite must cost nothing to run: no real AWS calls, no cloud
credentials, runs on free GitHub Actions minutes in the normal test job. Real
AWS coverage stays in the existing manual, gated `//go:build integration` suite
(unchanged) for whoever chooses to run it against their own account.

## Decisions (locked during brainstorming)

1. **Deliverable:** a CLI-level E2E harness with NO real AWS — not new
   integration tests, not just docs.
2. **CI:** the harness runs in the normal `go test ./...` CI job (no build tag).
   Real-AWS integration remains tagged + manual-only.
3. **Injection:** in-process. Register a fake `cloud.Provider` and drive the real
   `root.New` command tree against it. (A true-binary subprocess harness is
   explicitly deferred to a later sub-project.)
4. **Assertions:** the full triad — exit code + output + `state.json` — including
   cross-command flow (create → status sees it → cost prices it → destroy removes
   it).

## Why in-process against the real root command

`root.New(out)` wires every subcommand with `runtimeSource = runtimeStore.Require`
and resolves the provider in `PersistentPreRunE` via
`runtimeStore.Init(config.Path(...))` → `cloud.Get(cfg.Cloud.Provider, cfg)`.

So if a test:
- runs in a temp working dir (`t.Chdir(t.TempDir())`),
- writes a `fabrica.yaml` with `cloud.provider: fake` and an account/region,
- and the fake provider is registered under the name `"fake"`,

then real `root.New` + real `Init` + real arg parsing + real command wiring +
real state read/write to a temp `.fabrica/state.json` all execute unchanged. This
is the truest end-to-end we can get without compiling the binary — the only
substitution is the cloud provider at the registry boundary.

## Architecture

New black-box package: **`test/e2e/`** (`package e2e`), no `//go:build` tag →
included in `go test ./...` and CI.

Files:
- `test/e2e/fakeprovider_test.go` — the in-memory fake `cloud.Provider` +
  registration.
- `test/e2e/harness_test.go` — the `runCLI` helper + shared setup (temp dir,
  config writer, state reader).
- `test/e2e/<flow>_test.go` — one file per journey (or grouped logically).

### The fake provider

An in-memory provider registered in the e2e package's `init()`:

```go
func init() {
    cloud.Register("fake", func(cfg *config.Config) (cloud.Provider, error) {
        return newFakeProvider(cfg), nil
    })
}
```

It implements `Provider` plus every auxiliary interface the command tree type-
asserts, so all modules work against it:
- `cloud.Provider` — `Name`, `Identity`, `Resources`
- `cloud.ResourceClient` — `Create`/`Get`/`Update`/`Delete`/`List` over an
  in-memory `map[string]storedResource` keyed by identifier
- `cloud.EC2InstanceManager` — `StopInstance`/`StartInstance` flip an in-memory
  status field (for workstation stop/start)
- `cloud.StateBackendBootstrapper` / `StateBackendChecker` /
  `StateBackendDestroyer` — in-memory bucket/table existence + create/delete
- `cloud.CodeBuildRunner` — record `EnsureProject`/`DeleteProject`/build calls
- `cloud.GameLiftManager` — fleet status/events stubs sufficient for deploy flow

Behavior is deterministic and instant: `Create` assigns a deterministic
identifier (e.g. `fake-<type-slug>-<n>` from a counter) and stores the desired
state; `Get` returns stored actual state; `Delete` removes it; `List` enumerates
by type. No timing, no network, no randomness. `Identity` returns a fixed account
(matching the config) + region.

Verify against the real interfaces before finalizing: the fake must satisfy the
exact method sets in `internal/cloud/*.go`. A compile-time assertion
(`var _ cloud.GameLiftManager = (*fakeProvider)(nil)`, etc.) guards each.

### The harness helper

```go
// runCLI builds the real root command with a captured buffer, runs the args,
// and returns combined output + error. Each call is a fresh root (fresh Store)
// so PersistentPreRunE re-Inits from the temp fabrica.yaml.
func runCLI(t *testing.T, args ...string) (string, error)
```

Plus helpers: `writeConfig(t, dir)` (emits `fabrica.yaml` with `provider: fake`),
`readState(t)` (unmarshals `.fabrica/state.json` into `state.State`), and
assertion sugar (`assertContains`, `assertModuleAbsent`, etc.).

Each test starts with `t.Chdir(t.TempDir())` + `writeConfig` for isolation — no
shared state between tests, no touching the real repo `.fabrica`.

## Flows (one test each)

1. **First-run** — `setup --yes` → `status`. Assert: setup exits 0, state backend
   recorded, status reports backend healthy + no modules, exit 0.
2. **Perforce lifecycle (cross-command chain)** — `perforce create --yes` →
   `status` → `cost report` → `perforce destroy --yes` → `status`. Assert: create
   writes perforce module (instance + SG + volume) to state; status sees it; cost
   report prices the instance + volume (non-zero, expected lines); destroy removes
   the module; final status shows it gone.
3. **Workstation stop/start state machine** — `workstation create --yes` →
   `workstation stop` → `workstation start`. Assert: state status transitions
   `ready`/provisioning → `stopped` → `ready`; `cost report` drops the compute
   line while stopped and restores it after start; volume stays billed throughout.
4. **Full stack + teardown** — provision perforce + horde (+ one more module) →
   `cost report` (aggregate total across modules) → `destroy --all --yes`. Assert:
   all modules + backend removed from state; grand total reflected pre-teardown.
   Plus a **safety sub-case**: inject a fake delete failure on one module and
   assert `destroy --all` preserves the backend + returns an error naming the
   failed module (the backend-only-on-full-success invariant, at flow scale).
5. **JSON contract** — mid-flow, run `status --json` and `cost report --json`.
   Assert they `json.Unmarshal` cleanly and carry expected top-level fields
   (modules array, totals, etc.).

## Assertions — the triad, per step

- **Exit code:** `err == nil` on success; non-nil on the deliberate failure
  sub-case.
- **Output:** stdout contains expected human strings; `--json` steps unmarshal and
  carry expected fields.
- **State:** read `.fabrica/state.json` after each step; assert the right
  modules / resource types / statuses. This is the layer that catches
  state-tracking regressions the earlier 0%-coverage episode taught us to guard.

## Testing / verification

- The suite IS the tests; it must pass under `go test ./...` on Linux + Windows.
- Coverage: the e2e package exercises the command tree end-to-end; it will lift
  coverage on `cmd/*` wiring paths. It must itself meet the repo's ≥90% patch
  gate on any new non-test helper code (the fake provider is test code).
- `go build ./... && go vet ./... && gofmt -l . && golangci-lint run ./...` clean.
- Dependency rule preserved: `test/e2e` is a black-box consumer of `cmd/root` +
  `internal/cloud`; it must NOT cause `internal/cloud` to import `internal/state`
  or `internal/cost` (it doesn't — it only registers a provider).

## Out of scope (YAGNI)

- Real AWS calls / provisioning — that's the manual `//go:build integration`
  suite, unchanged.
- A compiled-binary subprocess harness — deferred (decided "in-process now,
  binary later").
- New CI workflow / AWS OIDC in CI — the suite rides the existing free test job.
- Testing provider-specific AWS translation (Cloud Control JSON shapes) — that's
  unit-tested in `internal/cloud/aws` already; the fake operates at the
  provider-interface level.

## Docs / roadmap updates on completion

- ROADMAP Milestone 5: check "End-to-end testing (broader E2E suite)" and note
  the CLI E2E harness (in-process, fake provider, CI-runnable).
- CLAUDE.md: add `test/e2e/` to the test-strategy section — what it is, that it
  runs in the default suite, and how to add a new flow.
