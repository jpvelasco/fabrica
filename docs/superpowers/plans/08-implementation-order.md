# Horde V1 — Recommended Implementation Order

Build bottom-up: pure functions first, then execution layer, then wiring.

---

## Phase 1: `internal/horde` plan layer

Complete this phase before touching any `cmd/` code.

- [ ] **Step 1: `HordeConfig` in `internal/config/config.go`**
  - Add `HordeConfig` struct next to `PerforceConfig`
  - Change `Config.Horde any` → `Config.Horde HordeConfig`
  - Update `fileConfig` struct and `fileConfig()` method
  - Remove `emptySection(c.Horde)` call
  - Run: `go build ./... && go test ./internal/config/...`

- [ ] **Step 2: `internal/horde/config.go`** — `VPCResolver` interface only

- [ ] **Step 3: `internal/horde/plan.go` + `plan_test.go`**
  - Write failing tests first (see `03-create-command.md` Task 1)
  - Implement `CreatePlan`, `NewCreatePlan`
  - Run: `go test ./internal/horde/... -run TestNewCreatePlan`

- [ ] **Step 4: `internal/horde/resources.go` + `resources_test.go`**
  - Write failing tests first
  - Implement `SGDesiredState`, `InstanceDesiredState`
  - Run: `go test ./internal/horde/... -run TestSGDesiredState -run TestInstanceDesiredState`

- [ ] **Step 5: `internal/horde/userdata.go` + `userdata_test.go`**
  - Write failing tests first
  - Implement `UserDataConfig`, `GenerateRaw`, `Generate`
  - Run: `go test ./internal/horde/... -run TestGenerate`

- [ ] **Step 6: `internal/horde/buildgraph/buildgraph.go` + `buildgraph_test.go`**
  - Write failing tests first
  - Implement `BuildGraphJob`, `ParseBuildGraph`
  - Run: `go test ./internal/horde/buildgraph/... -run TestParseBuildGraph`

- [ ] **Step 7: Extend `internal/perforce/cost.go` with m7i prices**
  - Add `m7i.xlarge`, `m7i.2xlarge`, `m7i.4xlarge`, `m7i.8xlarge` to `ec2InstancePrices`
  - Run: `go test ./internal/perforce/... && go test ./internal/horde/... ./internal/horde/buildgraph/...`

- [ ] **Step 8: Commit Phase 1**
  ```bash
  git add internal/config/config.go internal/horde/ internal/perforce/cost.go
  # internal/horde/ includes buildgraph/ sub-package
  git commit -m "feat: add internal/horde plan layer and HordeConfig"
  ```

### ✅ Checkpoint: internal/horde review

Before writing any `cmd/` code, verify:
- `go test ./internal/horde/... ./internal/horde/buildgraph/...` passes
- `go vet ./...` passes
- Coverage on `internal/horde/...` is at or above 60%
- All exported types match the spec in `02-module-structure.md`
- No circular imports: `go list -deps ./internal/horde/... | grep -v fabrica` should show only stdlib

Only proceed to Phase 2 once this checkpoint passes.

---

## Phase 2: `fabrica horde create`

- [ ] **Step 9: `cmd/horde/horde.go`** — parent command stub (no subcommands yet)
  ```go
  func New(...) *cobra.Command {
      cmd := &cobra.Command{Use: "horde", Short: "Manage Unreal Horde build coordinator"}
      return cmd
  }
  ```

- [ ] **Step 10: Wire `horde` into `cmd/root/root.go`**
  ```go
  cmd.AddCommand(horde.New(runtimeSource, optionsSource, out))
  ```
  Run: `go build ./...` — verify no compile errors

- [ ] **Step 11: `cmd/horde/create/create_test.go`** — write all white-box tests (failing)
  - See `06-testing-strategy.md` for full list
  - Use `fakeProvider` pattern from `06-testing-strategy.md`

- [ ] **Step 12: `cmd/horde/create/create.go`** — implement until tests pass
  - `command` struct with seams
  - `New()`, `run()`, `applyCreate()`
  - `printDryRun()`, `printApplyPlan()`, `printPostCreate()`
  - `generatePassword()`, `writeCredentials()`
  - Run: `go test ./cmd/horde/create/...`

- [ ] **Step 13: Add create to `cmd/horde/horde.go`**
  ```go
  cmd.AddCommand(create.New(runtimeSource, optionsSource, out))
  ```

- [ ] **Step 14: `cmd/horde/create/cobra_test.go`** — write and run cobra-layer tests

- [ ] **Step 15: Commit**
  ```bash
  git add cmd/horde/ cmd/root/root.go
  git commit -m "feat: add fabrica horde create command"
  ```

### ✅ Checkpoint: create command review + merge to main

Before starting status + submit, consider merging what exists into `main` as an intermediate PR:

- `go test ./...` passes
- `go build ./...` produces a working binary
- `./fabrica horde create --dry-run` shows correct plan output and cost estimate
- `./fabrica horde create --help` shows clean usage text
- `./fabrica horde status` (before status is implemented) shows appropriate "unknown command" or help text — not a panic
- Dry-run output includes m7i.2xlarge recommendation and `0.0.0.0/0` warning when applicable

**Merge point:** `internal/horde` plan layer + `fabrica horde create` together form a complete, testable unit. Merging here keeps PR size manageable and unblocks anyone building the AMI in parallel. Status and submit can land in a follow-on PR.

---

## Phase 3: `fabrica horde status`

- [ ] **Step 16: `cmd/horde/status/status_test.go`** — write all white-box tests (failing)
  - See `04-status-command.md` and `06-testing-strategy.md`

- [ ] **Step 17: `cmd/horde/status/status.go`** — implement until tests pass
  - `statusInfo`, `StatusOutput`, `command` struct
  - `New()`, `run()`, `buildInfo()`, `pollUntilReady()`
  - `printText()`, `printJSON()`, `parseInstanceActualState()`
  - Run: `go test ./cmd/horde/status/...`

- [ ] **Step 18: Add status to `cmd/horde/horde.go`**

- [ ] **Step 19: `cmd/horde/status/cobra_test.go`** — write and run cobra-layer tests

- [ ] **Step 20: Commit**
  ```bash
  git add cmd/horde/status/ cmd/horde/horde.go
  git commit -m "feat: add fabrica horde status command"
  ```

---

## Phase 4: `fabrica horde submit`

- [ ] **Step 21: `cmd/horde/submit/submit_test.go`** — write all white-box tests (failing)
  - See `05-submit-command.md` and `06-testing-strategy.md`

- [ ] **Step 22: `cmd/horde/submit/client.go`** — `HordeClient` interface + `hordeHTTPClient` stub**
  - Interface first (enough for tests to compile)
  - HTTP implementation can be minimal (tests inject `fakeHordeClient`)

- [ ] **Step 23: `cmd/horde/submit/submit.go`** — implement until tests pass
  - `command` struct with `hordeClient` seam
  - `New()`, `run()`, poll loop for `--wait`
  - Run: `go test ./cmd/horde/submit/...`

- [ ] **Step 24: Add submit to `cmd/horde/horde.go`**

- [ ] **Step 25: `cmd/horde/submit/cobra_test.go`** — write and run cobra-layer tests

- [ ] **Step 26: Commit**
  ```bash
  git add cmd/horde/submit/ cmd/horde/horde.go
  git commit -m "feat: add fabrica horde submit command"
  ```

### ✅ Checkpoint: status + submit review

- `go test ./cmd/horde/status/... ./cmd/horde/submit/...` passes
- `./fabrica horde status` on an empty state prints "not provisioned" cleanly
- `./fabrica horde submit --help` shows correct flag documentation
- JSON output from `./fabrica horde status --json` is valid and parseable
- `--wait` and `-w` short-form flags work for both commands

---

## Phase 5: Final checks

- [ ] **Step 27: Full test suite**
  ```bash
  go test ./...
  go build ./...
  go vet ./...
  golangci-lint run ./...
  ```

- [ ] **Step 28: Coverage check**
  ```bash
  go test -coverprofile=coverage.out ./internal/horde/... ./cmd/horde/...
  go tool cover -func=coverage.out | grep total
  ```
  Target: 60%+ on `internal/horde/`.

- [ ] **Step 29: Smoke test**
  ```bash
  ./fabrica horde --help
  ./fabrica horde create --dry-run
  ./fabrica horde status
  ```
  Verify help text, dry-run output, not-provisioned message.

- [ ] **Step 30: Final commit and push**
  ```bash
  git push origin docs/horde-v1-spec
  ```
  Open PR or update existing PR #8.

---

## Dependency Graph

```
internal/config  ← (HordeConfig added here)
       ↓
internal/horde            ← plan, resources, userdata
internal/horde/buildgraph ← BuildGraphJob, ParseBuildGraph (isolated sub-package)
       ↓
cmd/horde/create  →  cmd/horde/horde.go  →  cmd/root/root.go
cmd/horde/status  ↗
cmd/horde/submit  ↗  (imports internal/horde/buildgraph directly)
```

Each phase only depends on the phase above it. Complete Phase 1 entirely before writing any `cmd/horde/` code — the plan layer types must be stable before the execution layer references them.
