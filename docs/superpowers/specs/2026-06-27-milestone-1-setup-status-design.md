# Milestone 1: Foundation & First-Run Experience — Design

**Date:** 2026-06-27
**Branch:** `feat/milestone-1-setup-status`
**Status:** Draft — awaiting user approval

## Goal

Make Fabrica's first-run experience production-ready by delivering two commands:

1. **Real `fabrica setup`** — wire `state.Bootstrap()` to actually create the S3 state
   bucket (versioning, encryption, public access block) and the DynamoDB lock table,
   with dry-run, cost preview, idempotency, confirmation, and safety checks.
2. **Aggregate `fabrica status`** — a single read-only command showing health/overview
   across all active modules (Perforce, Horde, Workstation), with overall status,
   resource counts, and actionable next steps.

Both follow established Fabrica patterns: capability interfaces in `internal/cloud`,
seam-based dependency injection, two-package testing, dry-run, confirmation flows,
`fmt.Errorf("context: %w", ...)` error wrapping, and `fmt.Print*` output only.

## Locked Decisions

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| 1 | Setup confirmation | Simple y/N (`prompt.Confirm`); `--yes` skips | First real AWS write — a pause catches wrong account/region. Creation is idempotent and reversible, so the typed-phrase ceremony `destroy --all` uses is overkill. |
| 2 | Status readiness | State + EC2 only; `--probe` opts into TCP checks | Modules use private IPs (no public IP in V1). Always-probing hangs/times-out off-VPN. An overview command must be fast and never hang. |
| 3 | Status writes | Read-only | The per-module `<mod> status` commands own the `provisioning→ready` transition. An overview must not surprise-write `.fabrica/state.json`. |
| 4 | Integration tests | `//go:build integration` + `FABRICA_INTEGRATION=1` + account allowlist | Normal `go test ./...` and CI stay hermetic/mocked per CLAUDE.md. Real-AWS tests are opt-in, with unconditional cleanup. |

---

## Part A — Real `fabrica setup`

### What already exists (do not rebuild)

`cmd/setup/setup.go` already has: the plan layer (`state.NewSetupPlan`), dry-run output,
cost report, tag injection, account-ID persistence, and the apply scaffold. The only gap
is `state.Bootstrap()` returning `ErrBootstrapNotImplemented`. Cost estimators for
`AWS::S3::Bucket` and `AWS::DynamoDB::Table` are already registered in
`internal/cost/estimators_phase0.go`.

### New capability interface (`internal/cloud/state_backend.go`)

Mirror the existing `StateBackendChecker` / `StateBackendDestroyer` pattern:

```go
// StateBackendBootstrapper creates the storage primitives for Fabrica state.
type StateBackendBootstrapper interface {
    EnsureStateBucket(ctx context.Context, bucket, region string) (StateBackendCreateResult, error)
    EnsureStateLockTable(ctx context.Context, table string) (StateBackendCreateResult, error)
}

// StateBackendCreateResult describes one idempotent state-backend creation.
type StateBackendCreateResult struct {
    Identifier string
    Created    bool // false => already existed (idempotent no-op)
}
```

### AWS implementation (`internal/cloud/aws/state_backend.go`)

`awsProvider` gains `EnsureStateBucket` / `EnsureStateLockTable`, reusing the existing
`s3StateClient` / `dynamoDBStateClient` seam factories. The `stateBackendS3Client` and
`stateBackendDynamoDBClient` interfaces widen to add the create/configure calls, plus a
table-exists waiter factory (parallel to the existing not-exists waiters):

- `stateBackendS3Client` adds: `CreateBucket`, `PutBucketVersioning`,
  `PutBucketEncryption`, `PutPublicAccessBlock` (keeps existing `HeadBucket`, `DeleteBucket`).
- `stateBackendDynamoDBClient` adds: `CreateTable` (keeps `DescribeTable`, `DeleteTable`).
- New `stateBackendTableExistsWaiter` factory wrapping `dynamodb.NewTableExistsWaiter`.

**S3 bucket sequence** (`EnsureStateBucket`), each step idempotent:

1. `HeadBucket` → if it exists and we own it: `Created=false`, still run steps 3–5 to
   reconcile configuration (versioning/encryption/PAB) so re-running `setup` heals drift.
2. `CreateBucket` — include `CreateBucketConfiguration.LocationConstraint` for every
   region **except** `us-east-1` (AWS rejects the constraint there).
3. `PutBucketVersioning` → `Status: Enabled`.
4. `PutBucketEncryption` → SSE-KMS with `cfg.State.KMSKeyID` when set, else SSE-S3 (`AES256`).
5. `PutPublicAccessBlock` → all four flags `true`.

**DynamoDB sequence** (`EnsureStateLockTable`):

1. `DescribeTable` → if present: `Created=false`, return.
2. `CreateTable` — hash key `LockID` (type `S`), `BillingMode: PAY_PER_REQUEST`.
3. Wait via `TableExists` waiter until `ACTIVE` (bounded timeout, e.g. 2 min).

All operations carry their own `context.WithTimeout` like the existing checker/destroyer.

### `state.Bootstrap()` rewrite (`internal/state/bootstrap.go`)

Remove `ErrBootstrapNotImplemented`. New flow:

1. `provider.Identity(ctx)` first (surface credential problems immediately) — unchanged.
2. Type-assert `provider.(cloud.StateBackendBootstrapper)`; if unsupported, return a
   clear error naming the provider.
3. Resolve `BackendNames` (bucket+table) from config/account.
4. `EnsureStateBucket` then `EnsureStateLockTable`, in order. Each maps to a
   `BootstrapResult{Name, Existed: !Created}`. Bucket failure → return before table
   (partial-failure recoverable: re-running heals).

### `cmd/setup` changes

Keep dry-run, cost, tags output. Replace the `ErrBootstrapNotImplemented` branch:

- After `printApplyHeader`: if not `--yes`, prompt `prompt.Confirm` ("Create these
  resources? [y/N]"). Decline → print "Setup cancelled. No AWS resources were created."
  and return nil.
- Call `bootstrap`; on success print per-resource results (`created` / `already exists`),
  save account ID (existing `saveAccountID`), print completion + next steps.
- Delete `printNotImplementedWarning` and the `docs/setup-manual.md` pointer.
- Rewrite the cobra `Long` text to describe real provisioning.

---

## Part B — Aggregate `fabrica status`

### New package `cmd/status` (top-level, wired in `cmd/root`)

Read-only overview. `New(runtimeSource, optionsSource, out)` returns the cobra command;
a `--probe` bool flag is local to the command. Flow (`command.run`):

1. Read `.fabrica/state.json` via `ReadStateOrNew` (seam: `readState`). No modules →
   friendly empty-state message + next steps (`fabrica setup`, `fabrica perforce create`,
   `fabrica horde create`, `fabrica workstation create`). Return nil.
2. **Backend health:** type-assert `Provider` to `cloud.StateBackendChecker` (same calls
   as `doctor`) → report bucket + table existence. Unavailable provider → "unknown".
3. **Per module** in state: name, version, status, resource count, key resource IDs
   (instance, SG via `stateutil.ResourceByType`). When the provider exposes a
   `ResourceClient`, query EC2 instance state via Cloud Control `Get` (seam:
   `getResource`) for live EC2 state (running/stopped/etc.). Cloud Control failure for one
   module degrades gracefully (show state-file status, note "live state unavailable").
4. `--probe` (default off): TCP-probe each module's readiness port via
   `modstatus.DefaultProbeTCP` (seam: `probeTCP`). Port lookup is a local
   `map[string]int` in `cmd/status` (`perforce`:1666, `horde`:5000, `workstation`:8443).
5. **Read-only** — never calls `WriteState`.

### Output

**Text:** header, backend line, one line per module
(`STATUS  module  version  N resources  [ec2: running]  [probe: responding|unreachable]`),
then a summary footer (`N modules · M resources · backend: ok|not provisioned`) and an
**actionable next steps** block derived from module states (e.g. a `provisioning` module →
"run `fabrica perforce status` to watch it become ready"; no backend → "run `fabrica setup`").

**JSON (`--json`):** `StatusReport{ Backend StatusBackend, Modules []StatusModule, Summary StatusSummary }`,
marshalled with `json.MarshalIndent`.

### Why not reuse the modstatus engine wholesale

`modstatus.Command` is single-module and write-capable (transitions + polls). Aggregate
status is multi-module, read-only, no-poll. It reuses the leaf helpers that fit
(`DefaultProbeTCP`, `stateutil.ResourceByType`, the EC2 `ActualState` parse shape) without
adopting the engine's lifecycle. This matches the CLAUDE.md guidance: share substance, not
shape — don't force an engine where responsibilities differ.

---

## Testing

### Unit (hermetic, runs in default `go test ./...` and CI)

**`internal/cloud/aws/state_backend_test.go`** (extend existing) — fake S3/DynamoDB
clients covering:
- Fresh create: bucket created + versioning/encryption/PAB applied; table created + waited.
- Idempotent: `HeadBucket`/`DescribeTable` report existing → `Created=false`; config still
  reconciled on the bucket.
- Region location constraint: `us-east-1` omits constraint; other region includes it.
- Encryption: KMS when `KMSKeyID` set, SSE-S3 otherwise.
- Error propagation: `CreateBucket` / `PutPublicAccessBlock` / `CreateTable` failures wrap.

**`internal/state/bootstrap_test.go`** (new) — fake provider implementing
`StateBackendBootstrapper`: success path → two results; provider lacking the interface →
clear error; bucket failure → table not attempted; identity failure → surfaced first.

**`cmd/setup/setup_test.go`** (extend) — white-box `run()` with injected `bootstrap` seam +
confirm seam: dry-run unchanged; confirm-yes applies; confirm-no cancels with no bootstrap
call; `--yes` skips prompt; bootstrap error propagates; results render correctly.
**`cmd/setup/cobra_test.go`** (extend) — black-box `--dry-run`, `--yes` via minimal root.

**`cmd/status/status_test.go`** (new) — white-box `run()` with seams
(`readState`, `getResource`, `probeTCP`, backend checker fake): empty state; single/multiple
modules; backend present/absent; `--probe` on/off; Cloud Control failure degrades; `--json`
shape. **`cmd/status/cobra_test.go`** (new) — black-box with minimal root replicating
`--json` / `--probe` hierarchy.

### Integration (opt-in, excluded from default test run and CI)

**`internal/cloud/aws/state_backend_integration_test.go`** — `//go:build integration`.
Skips unless `FABRICA_INTEGRATION=1`. Requires `FABRICA_INTEGRATION_ACCOUNT` to match the
live `sts:GetCallerIdentity` account (guard against wrong account). Creates a
uniquely-named bucket + table, asserts versioning=Enabled, encryption present, all four PAB
flags true, table `ACTIVE` with `LockID` key; **`t.Cleanup` deletes both unconditionally**
(empty + delete bucket, delete table). Documented run command in the test file header.

### Coverage

Target ≥60% for touched `internal/*` packages (CLAUDE.md). No new cost estimators.

---

## Docs & cross-cutting

- `ROADMAP.md` — mark `setup` and aggregate `status` implemented; update Phase 1 status.
- `CLAUDE.md` — replace the "`fabrica setup` is intentionally a no-op" paragraph with the
  real behavior; add a `cmd/status` row to the package table; note the
  `StateBackendBootstrapper` interface alongside Checker/Destroyer.
- `cmd/setup` `Long` help — rewrite for real provisioning.
- Remove/redirect the stale `docs/setup-manual.md` pointer in setup output.

## CI note

Bootstrapper correctness is covered by mocked unit tests; real CI is not expected to be
needed. If something genuinely cannot be verified locally, use the (user-authorized)
private→public→test→private repo flip as a last resort and flip back immediately.

## Out of scope (remains for later Phase 1 milestones)

- `fabrica setup` as a full guided multi-module wizard (this milestone does the state
  backend only — the documented Phase 0 scope).
- CI/CD (`fabrica ci`), deploy (`fabrica deploy`), cost reporting (`fabrica cost`) commands.
- Remote state read in `status` (reads local `.fabrica/state.json` cache only).
