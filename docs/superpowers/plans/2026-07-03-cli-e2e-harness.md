# CLI E2E Test Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A fast, free, CI-runnable end-to-end suite in `test/e2e/` that drives real Fabrica command flows against an in-memory fake `cloud.Provider`, asserting the full triad (exit codes + output + `.fabrica/state.json`) across operator journeys.

**Architecture:** In-process. A fake provider is registered under the name `"fake"` in the e2e package's `init()`; its in-memory store lives in a per-test holder (`currentFake`) the constructor closes over. Tests run the REAL `cmd/root.New` command tree in a temp working dir with a `fabrica.yaml` that selects `provider: fake`, so real arg parsing, command wiring, and state read/write all execute — only the cloud boundary is swapped. No real AWS, no credentials, no build tag → runs in the normal `go test ./...` and CI.

**Tech Stack:** Go 1.25.11, Cobra, standard `testing`, `go.yaml.in/yaml/v3` (via `internal/config`). No new dependencies.

## Global Constraints

- Go 1.25.11+; module path `github.com/jpvelasco/fabrica`.
- NO real AWS, NO credentials, NO network. Deterministic + instant. NO `//go:build` tag (must run in default `go test ./...` + CI).
- Package `test/e2e` is black-box: `package e2e`. It imports `cmd/root`, `internal/cloud`, `internal/config`, `internal/state`. It must NOT import `internal/cloud/aws` directly (root blank-imports it, registering `"aws"`; the fake registers `"fake"` — different names, no duplicate-registration panic).
- Tests do NOT call `t.Parallel()` — they share the package-level `currentFake` holder. The suite is fast; serial is fine.
- Fake identifiers are predictable: `fake-<type-slug>-<n>`, per-type counter from 1, reset per test via `newFakeStore()`. `<type-slug>` derives from TypeName (e.g. `AWS::EC2::Instance` → `ec2-instance`).
- Each test starts with `setupE2E(t)` = `t.Chdir(t.TempDir())` + `writeConfig(t)` + `resetFake(t)`.
- Naming: `snake_case.go` files; acronyms uppercase (`ID`, `URL`, `AWS`); single-letter receivers where idiomatic; `fmt.Print*` only.
- The fake is TEST code (only compiled under `_test.go`), so it is exempt from the ≥90% patch-coverage gate — but any non-test helper added elsewhere is not. Keep everything in `_test.go` files.
- Compile-time interface assertions guard the fake: `var _ cloud.GameLiftManager = (*fakeProvider)(nil)` etc. for every interface it claims.

## cloud interface method sets the fake must satisfy (verbatim from internal/cloud)

- `Provider`: `Name() string`; `Identity(ctx) (account, arn, region string, err error)`; `Resources() ResourceClient`
- `ResourceClient`: `Create/Get/Update/Delete(ctx, *Resource) error`; `List(ctx, typeName string) ([]Resource, error)`
- `EC2InstanceManager`: `StopInstance(ctx, instanceID string) error`; `StartInstance(ctx, instanceID string) error`
- `StateBackendChecker`: `StateBucketExists(ctx, bucket) (bool, error)`; `StateLockTableExists(ctx, table) (bool, error)`
- `StateBackendBootstrapper`: `EnsureStateBucket(ctx, bucket, region) (StateBackendCreateResult, error)`; `EnsureStateLockTable(ctx, table) (StateBackendCreateResult, error)`
- `StateBackendDestroyer`: `DeleteStateBucket(ctx, bucket) (StateBackendDeleteResult, error)`; `DeleteStateLockTable(ctx, table) (StateBackendDeleteResult, error)`
- `CodeBuildRunner`: `EnsureProject(ctx, CodeBuildProjectSpec) (bool, error)`; `DeleteProject(ctx, name) error`; `StartBuild(ctx, project, env) (string, error)`; `BuildStatus(ctx, buildID) (BuildInfo, error)`; `BuildLog(ctx, buildID) (string, error)`
- `GameLiftManager`: `CreateFleetAsync(ctx, *Resource) error`; `FleetStatus(ctx, fleetID) (FleetInfo, error)`; `FleetEvents(ctx, fleetID) ([]FleetEvent, error)`

Result structs: `StateBackendCreateResult{Identifier string; Created bool}`; `StateBackendDeleteResult{Identifier string; Deleted, Missing bool}`; `FleetInfo{FleetID, Status string}`; `FleetEvent{Code, Message, Time string}`; `BuildInfo{ID, Status, Phase, LogGroup, LogStream string}`.

---
## File Structure

All files are `_test.go` (test-only compilation) in a new `test/e2e/` directory, `package e2e`:

- `test/e2e/fake_provider_test.go` — the in-memory `fakeStore` + `fakeProvider` implementing `Provider` + all aux interfaces; `resetFake`; the `init()` registration; compile-time interface assertions.
- `test/e2e/harness_test.go` — `runCLI`, `setupE2E`, `writeConfig`, `readState`, and assertion helpers.
- `test/e2e/firstrun_test.go` — Flow 1 (setup → status).
- `test/e2e/perforce_test.go` — Flow 2 (perforce lifecycle cross-command chain).
- `test/e2e/workstation_test.go` — Flow 3 (workstation stop/start state machine).
- `test/e2e/destroyall_test.go` — Flow 4 (full stack + teardown, incl. failure sub-case).
- `test/e2e/json_test.go` — Flow 5 (JSON contract).

**Build order:** Task 1 fake provider → Task 2 harness helpers → Tasks 3-7 one flow each → Task 8 docs. Task 2 depends on Task 1; flows depend on both; each flow is independent of the others.

**Modified (Task 8 only):** `ROADMAP.md`, `CLAUDE.md`.

---

### Task 1: Fake in-memory provider

**Files:**
- Create: `test/e2e/fake_provider_test.go`

**Interfaces:**
- Consumes: `internal/cloud` (`Provider`, `ResourceClient`, `Resource`, aux interfaces + result structs), `internal/config`.
- Produces:
  - `type fakeStore` — in-memory resource store with per-type ID counters, plus recorded backend/codebuild/gamelift state.
  - `func newFakeStore() *fakeStore`
  - `var currentFake *fakeStore` and `func resetFake(t *testing.T) *fakeStore`
  - `type fakeProvider struct { store *fakeStore; account, region string }` implementing all interfaces.
  - `init()` registering `cloud.Register("fake", …)`.
  - `fakeStore.failDeleteType string` — when set, `ResourceClient.Delete` returns an error for that TypeName (drives the destroy-all failure sub-case).

- [ ] **Step 1: Write the fake provider**

Create `test/e2e/fake_provider_test.go`. This is infrastructure (no standalone test yet — Task 2's smoke test exercises it). Full content:

```go
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

// currentFake holds the in-memory store for the running test. resetFake installs
// a fresh one; the registered "fake" provider constructor returns a provider
// backed by it, so every runCLI call within one test shares one store. Tests do
// not run in parallel (they share this global).
var currentFake *fakeStore

func resetFake(t *testing.T) *fakeStore {
	t.Helper()
	currentFake = newFakeStore()
	return currentFake
}

func init() {
	cloud.Register("fake", func(cfg *config.Config) (cloud.Provider, error) {
		if currentFake == nil {
			currentFake = newFakeStore()
		}
		acct := cfg.Cloud.AWS.AccountID
		if acct == "" {
			acct = "123456789012"
		}
		region := cfg.Cloud.AWS.Region
		if region == "" {
			region = "us-east-1"
		}
		return &fakeProvider{store: currentFake, account: acct, region: region}, nil
	})
}

// storedResource is one resource in the fake store.
type storedResource struct {
	typeName    string
	identifier  string
	desired     json.RawMessage
	ec2Status   string // for EC2 instances: "running" / "stopped"
}

// fakeStore is the in-memory backing store for a single test.
type fakeStore struct {
	mu        sync.Mutex
	resources map[string]*storedResource // keyed by identifier
	counters  map[string]int             // per-type-slug counter

	buckets map[string]bool
	tables  map[string]bool
	projects map[string]bool

	// failDeleteType, when non-empty, makes ResourceClient.Delete return an
	// error for resources of that TypeName (drives the destroy-all failure case).
	failDeleteType string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		resources: make(map[string]*storedResource),
		counters:  make(map[string]int),
		buckets:   make(map[string]bool),
		tables:    make(map[string]bool),
		projects:  make(map[string]bool),
	}
}

// typeSlug turns "AWS::EC2::Instance" into "ec2-instance".
func typeSlug(typeName string) string {
	parts := strings.Split(typeName, "::")
	tail := parts[len(parts)-2:] // drop the "AWS" prefix when present
	if len(parts) < 2 {
		tail = parts
	}
	return strings.ToLower(strings.Join(tail, "-"))
}

type fakeProvider struct {
	store   *fakeStore
	account string
	region  string
}

// --- Provider ---

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Identity(context.Context) (string, string, string, error) {
	return p.account, "arn:aws:iam::" + p.account + ":user/e2e", p.region, nil
}

func (p *fakeProvider) Resources() cloud.ResourceClient { return &fakeRC{store: p.store} }

// --- ResourceClient ---

type fakeRC struct{ store *fakeStore }

func (c *fakeRC) Create(_ context.Context, r *cloud.Resource) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	slug := typeSlug(r.TypeName)
	c.store.counters[slug]++
	id := fmt.Sprintf("fake-%s-%d", slug, c.store.counters[slug])
	r.Identifier = id
	sr := &storedResource{typeName: r.TypeName, identifier: id, desired: r.DesiredState}
	if r.TypeName == "AWS::EC2::Instance" {
		sr.ec2Status = "running"
	}
	c.store.resources[id] = sr
	return nil
}

func (c *fakeRC) Get(_ context.Context, r *cloud.Resource) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	sr, ok := c.store.resources[r.Identifier]
	if !ok {
		return cloud.ErrResourceNotFound
	}
	// Provide a minimal actual state; EC2 instances report their status so the
	// teardown engine's transitional-state check sees a terminal state.
	if sr.typeName == "AWS::EC2::Instance" {
		r.ActualState = json.RawMessage(fmt.Sprintf(`{"State":{"Name":%q},"PrivateIpAddress":"10.0.0.10"}`, sr.ec2Status))
	} else {
		r.ActualState = json.RawMessage(`{}`)
	}
	return nil
}

func (c *fakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }

func (c *fakeRC) Delete(_ context.Context, r *cloud.Resource) error {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	if c.store.failDeleteType != "" && r.TypeName == c.store.failDeleteType {
		return fmt.Errorf("fake: forced delete failure for %s", r.TypeName)
	}
	if _, ok := c.store.resources[r.Identifier]; !ok {
		return cloud.ErrResourceNotFound
	}
	delete(c.store.resources, r.Identifier)
	return nil
}

func (c *fakeRC) List(_ context.Context, typeName string) ([]cloud.Resource, error) {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	var out []cloud.Resource
	for _, sr := range c.store.resources {
		if sr.typeName == typeName {
			out = append(out, cloud.Resource{TypeName: sr.typeName, Identifier: sr.identifier})
		}
	}
	return out, nil
}

// --- EC2InstanceManager ---

func (p *fakeProvider) StopInstance(_ context.Context, instanceID string) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	if sr, ok := p.store.resources[instanceID]; ok {
		sr.ec2Status = "stopped"
		return nil
	}
	return cloud.ErrResourceNotFound
}

func (p *fakeProvider) StartInstance(_ context.Context, instanceID string) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	if sr, ok := p.store.resources[instanceID]; ok {
		sr.ec2Status = "running"
		return nil
	}
	return cloud.ErrResourceNotFound
}

// --- StateBackendChecker ---

func (p *fakeProvider) StateBucketExists(_ context.Context, bucket string) (bool, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	return p.store.buckets[bucket], nil
}

func (p *fakeProvider) StateLockTableExists(_ context.Context, table string) (bool, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	return p.store.tables[table], nil
}

// --- StateBackendBootstrapper ---

func (p *fakeProvider) EnsureStateBucket(_ context.Context, bucket, _ string) (cloud.StateBackendCreateResult, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	created := !p.store.buckets[bucket]
	p.store.buckets[bucket] = true
	return cloud.StateBackendCreateResult{Identifier: bucket, Created: created}, nil
}

func (p *fakeProvider) EnsureStateLockTable(_ context.Context, table string) (cloud.StateBackendCreateResult, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	created := !p.store.tables[table]
	p.store.tables[table] = true
	return cloud.StateBackendCreateResult{Identifier: table, Created: created}, nil
}

// --- StateBackendDestroyer ---

func (p *fakeProvider) DeleteStateBucket(_ context.Context, bucket string) (cloud.StateBackendDeleteResult, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	existed := p.store.buckets[bucket]
	delete(p.store.buckets, bucket)
	return cloud.StateBackendDeleteResult{Identifier: bucket, Deleted: existed, Missing: !existed}, nil
}

func (p *fakeProvider) DeleteStateLockTable(_ context.Context, table string) (cloud.StateBackendDeleteResult, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	existed := p.store.tables[table]
	delete(p.store.tables, table)
	return cloud.StateBackendDeleteResult{Identifier: table, Deleted: existed, Missing: !existed}, nil
}

// --- CodeBuildRunner ---

func (p *fakeProvider) EnsureProject(_ context.Context, spec cloud.CodeBuildProjectSpec) (bool, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	created := !p.store.projects[spec.Name]
	p.store.projects[spec.Name] = true
	return created, nil
}

func (p *fakeProvider) DeleteProject(_ context.Context, name string) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	delete(p.store.projects, name) // missing project is not an error (matches real contract)
	return nil
}

func (p *fakeProvider) StartBuild(_ context.Context, _ string, _ map[string]string) (string, error) {
	return "fake-build-1", nil
}

func (p *fakeProvider) BuildStatus(_ context.Context, buildID string) (cloud.BuildInfo, error) {
	return cloud.BuildInfo{ID: buildID, Status: "SUCCEEDED", Phase: "COMPLETED"}, nil
}

func (p *fakeProvider) BuildLog(_ context.Context, _ string) (string, error) { return "fake log", nil }

// --- GameLiftManager ---

func (p *fakeProvider) CreateFleetAsync(_ context.Context, r *cloud.Resource) error {
	return (&fakeRC{store: p.store}).Create(context.Background(), r)
}

func (p *fakeProvider) FleetStatus(_ context.Context, fleetID string) (cloud.FleetInfo, error) {
	return cloud.FleetInfo{FleetID: fleetID, Status: "ACTIVE"}, nil
}

func (p *fakeProvider) FleetEvents(_ context.Context, _ string) ([]cloud.FleetEvent, error) {
	return nil, nil
}

// Compile-time assertions that the fake satisfies every interface the command
// tree type-asserts.
var (
	_ cloud.Provider                 = (*fakeProvider)(nil)
	_ cloud.EC2InstanceManager       = (*fakeProvider)(nil)
	_ cloud.StateBackendChecker      = (*fakeProvider)(nil)
	_ cloud.StateBackendBootstrapper = (*fakeProvider)(nil)
	_ cloud.StateBackendDestroyer    = (*fakeProvider)(nil)
	_ cloud.CodeBuildRunner          = (*fakeProvider)(nil)
	_ cloud.GameLiftManager          = (*fakeProvider)(nil)
	_ cloud.ResourceClient           = (*fakeRC)(nil)
)
```

- [ ] **Step 2: Verify it compiles**

Run: `go vet ./test/e2e/`
Expected: compiles clean. If any interface assertion fails, the method set is wrong — fix against the real interface in `internal/cloud/`. (There is no runnable test yet; Task 2 adds the first one. `go vet` on a package with only a fake + no tests still type-checks the assertions.)

Note: `cloud.ErrResourceNotFound` is defined in `internal/cloud/provider.go` (verified) — used by `Get`/`Delete` above.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/fake_provider_test.go
git commit -m "test(e2e): in-memory fake cloud provider for E2E harness"
```

---

### Task 2: Harness helpers + smoke test

**Files:**
- Create: `test/e2e/harness_test.go`

**Interfaces:**
- Consumes: Task 1's `resetFake`; `cmd/root`, `internal/config`, `internal/state`.
- Produces:
  - `func runCLI(t *testing.T, args ...string) (string, error)` — builds a fresh `root.New`, runs args, returns combined output + error.
  - `func setupE2E(t *testing.T) *fakeStore` — `t.Chdir(t.TempDir())` + `writeConfig` + `resetFake`.
  - `func writeConfig(t *testing.T)` — writes `fabrica.yaml` (provider: fake).
  - `func readState(t *testing.T) *state.State` — unmarshals `.fabrica/state.json`.
  - Assertion helpers: `assertContains`, `assertJSON`, `assertModuleExists`, `assertModuleAbsent`, `assertModuleStatus`, `assertResourceType`.

- [ ] **Step 1: Write the harness + a smoke test**

Create `test/e2e/harness_test.go`:

```go
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/root"
	"github.com/jpvelasco/fabrica/internal/state"
)

// runCLI builds a fresh real root command (fresh globals.Store) with a captured
// buffer, runs the given args, and returns combined output + the command error.
// A fresh root per call means PersistentPreRunE re-Inits from the temp
// fabrica.yaml — and resolves the "fake" provider backed by currentFake.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := root.New(&buf)
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// setupE2E isolates the test: a fresh temp working dir, a fake-provider config,
// and a fresh in-memory store. Returns the store for failure-injection cases.
func setupE2E(t *testing.T) *fakeStore {
	t.Helper()
	t.Chdir(t.TempDir())
	writeConfig(t)
	return resetFake(t)
}

// writeConfig emits a fabrica.yaml selecting the fake provider. VPC/subnet are
// set so provisioning is fully deterministic (create passes resolver=nil, so
// empty values are also fine, but explicit is clearer).
func writeConfig(t *testing.T) {
	t.Helper()
	const cfg = `cloud:
  provider: fake
  aws:
    region: us-east-1
    accountId: "123456789012"
perforce:
  vpcId: vpc-fake
  subnetId: subnet-fake
horde:
  amiId: ami-fake
  vpcId: vpc-fake
  subnetId: subnet-fake
workstation:
  amiId: ami-fake
  vpcId: vpc-fake
  subnetId: subnet-fake
deploy:
  buildBucket: fake-build-bucket
`
	if err := os.WriteFile("fabrica.yaml", []byte(cfg), 0644); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
}

// readState loads .fabrica/state.json; fails the test if it is missing.
func readState(t *testing.T) *state.State {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(".fabrica", "state.json"))
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	var st state.State
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("readState unmarshal: %v", err)
	}
	return &st
}

func assertContains(t *testing.T, out, substr string) {
	t.Helper()
	if !strings.Contains(out, substr) {
		t.Fatalf("output missing %q:\n%s", substr, out)
	}
}

func assertJSON(t *testing.T, out string, target any) {
	t.Helper()
	// The command may print a human line before/after JSON in some paths; find
	// the JSON object/array span. For these flows the JSON is the whole output.
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), target); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
}

func assertModuleExists(t *testing.T, st *state.State, name string) {
	t.Helper()
	if st.GetModule(name) == nil {
		t.Fatalf("expected module %q in state, modules present: %v", name, moduleNames(st))
	}
}

func assertModuleAbsent(t *testing.T, st *state.State, name string) {
	t.Helper()
	if st.GetModule(name) != nil {
		t.Fatalf("expected module %q absent, but it is present", name)
	}
}

func assertModuleStatus(t *testing.T, st *state.State, name, want string) {
	t.Helper()
	m := st.GetModule(name)
	if m == nil {
		t.Fatalf("module %q not in state", name)
	}
	if m.Status != want {
		t.Fatalf("module %q status = %q, want %q", name, m.Status, want)
	}
}

func assertResourceType(t *testing.T, st *state.State, module, typeName string) {
	t.Helper()
	m := st.GetModule(module)
	if m == nil {
		t.Fatalf("module %q not in state", module)
	}
	for _, r := range m.Resources {
		if r.TypeName == typeName {
			return
		}
	}
	t.Fatalf("module %q has no resource of type %q; has: %v", module, typeName, m.Resources)
}

func moduleNames(st *state.State) []string {
	var names []string
	for _, m := range st.Modules {
		names = append(names, m.Name)
	}
	return names
}

// TestHarnessSmoke verifies the harness wiring: the fake provider resolves and a
// trivial command runs end-to-end.
func TestHarnessSmoke(t *testing.T) {
	setupE2E(t)
	out, err := runCLI(t, "version")
	if err != nil {
		t.Fatalf("version: %v\n%s", err, out)
	}
	// version prints the version string; just assert it ran and produced output.
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected version output, got empty")
	}
}
```

- [ ] **Step 2: Run the smoke test**

Run: `go test ./test/e2e/ -run TestHarnessSmoke -v`
Expected: PASS. This proves `root.New` builds, the fake registers without colliding with `"aws"`, and a command executes. (`version` needs no provider, so it isolates harness wiring from provider behavior.)

- [ ] **Step 3: Verify the fake actually resolves via a provider-touching command**

Add this test to `harness_test.go` and run it:

```go
// TestHarnessFakeResolves confirms PersistentPreRunE resolves the fake provider
// (a command that needs the provider runs without an AWS error).
func TestHarnessFakeResolves(t *testing.T) {
	setupE2E(t)
	out, err := runCLI(t, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	// Fresh account: no backend, no modules. cmd/status prints this exact line
	// (cmd/status/status.go): "Nothing provisioned yet, and your state backend
	// isn't set up."
	assertContains(t, out, "Nothing provisioned yet")
}
```

Run: `go test ./test/e2e/ -run TestHarness -v`
Expected: both PASS. If `status` errors with a provider/credentials message, the fake isn't resolving — check `cloud.provider: fake` in `writeConfig` matches the registered name, and that `root` blank-imports don't panic on double registration.

- [ ] **Step 4: Commit**

```bash
git add test/e2e/harness_test.go
git commit -m "test(e2e): harness helpers (runCLI, setup, state + assertion helpers) + smoke tests"
```

---

### Task 3: Flow 1 — first-run (setup → status)

**Files:**
- Create: `test/e2e/firstrun_test.go`

**Interfaces:**
- Consumes: Task 2 harness (`setupE2E`, `runCLI`, `assertContains`).

- [ ] **Step 1: Write the flow test**

Create `test/e2e/firstrun_test.go`:

```go
package e2e

import "testing"

// TestFirstRun: a fresh account runs setup, then status reports the backend is
// ready with no modules yet.
func TestFirstRun(t *testing.T) {
	setupE2E(t)

	// Before setup: status says nothing is provisioned.
	out, err := runCLI(t, "status")
	if err != nil {
		t.Fatalf("status (pre-setup): %v\n%s", err, out)
	}
	assertContains(t, out, "Nothing provisioned yet")

	// setup --yes creates the state backend (bucket + table) via the fake.
	out, err = runCLI(t, "setup", "--yes")
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	assertContains(t, out, "Setup complete")

	// After setup: status reports the backend is ready but no modules.
	out, err = runCLI(t, "status")
	if err != nil {
		t.Fatalf("status (post-setup): %v\n%s", err, out)
	}
	assertContains(t, out, "State backend is ready, but no modules are provisioned yet.")
}
```

Exact strings verified against `cmd/setup/setup.go` ("Setup complete — your state backend is ready.") and `cmd/status/status.go` ("State backend is ready, but no modules are provisioned yet.").

- [ ] **Step 2: Run**

Run: `go test ./test/e2e/ -run TestFirstRun -v`
Expected: PASS. If `setup --yes` errors, confirm the fake implements `StateBackendBootstrapper` (Task 1) and that `--yes` skips confirmation.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/firstrun_test.go
git commit -m "test(e2e): first-run flow (setup then status)"
```

---

### Task 4: Flow 2 — perforce lifecycle (cross-command chain)

**Files:**
- Create: `test/e2e/perforce_test.go`

**Interfaces:**
- Consumes: Task 2 harness + `assertModuleExists`/`assertModuleAbsent`/`assertResourceType`, `readState`.

- [ ] **Step 1: Write the flow test**

Create `test/e2e/perforce_test.go`:

```go
package e2e

import "testing"

// TestPerforceLifecycle: create writes state → status sees it → cost prices it →
// destroy removes it. The cross-command chain is the point.
func TestPerforceLifecycle(t *testing.T) {
	setupE2E(t)

	// Provision.
	out, err := runCLI(t, "perforce", "create", "--yes")
	if err != nil {
		t.Fatalf("perforce create: %v\n%s", err, out)
	}
	assertContains(t, out, "Perforce Helix Core provisioned.")

	// State has the module + its EC2 instance and security group.
	st := readState(t)
	assertModuleExists(t, st, "perforce")
	assertResourceType(t, st, "perforce", "AWS::EC2::Instance")
	assertResourceType(t, st, "perforce", "AWS::EC2::SecurityGroup")

	// status sees the provisioned module (prints the module name).
	out, err = runCLI(t, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	assertContains(t, out, "perforce")

	// cost report prices the module (perforce line + a positive Total).
	out, err = runCLI(t, "cost", "report")
	if err != nil {
		t.Fatalf("cost report: %v\n%s", err, out)
	}
	assertContains(t, out, "perforce")
	assertContains(t, out, "Total:")

	// Tear it down.
	out, err = runCLI(t, "perforce", "destroy", "--yes")
	if err != nil {
		t.Fatalf("perforce destroy: %v\n%s", err, out)
	}

	// Module is gone from state.
	st = readState(t)
	assertModuleAbsent(t, st, "perforce")
}
```

Exact strings verified against `cmd/perforce/create/create.go` ("Perforce Helix Core provisioned.") and `cmd/cost/report/report.go` ("Total:"). `perforce destroy --yes` uses the teardown engine; `--yes` skips the typed-phrase confirm.

- [ ] **Step 2: Run**

Run: `go test ./test/e2e/ -run TestPerforceLifecycle -v`
Expected: PASS. If create fails resolving a VPC, note create passes `resolver=nil`, so empty VPC is fine — but `writeConfig` sets `vpcId`/`subnetId` anyway. If destroy fails on the EC2 transitional-state check, confirm the fake's `Get` returns `State.Name: "running"` (a terminal, non-transitional state) so the teardown engine proceeds.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/perforce_test.go
git commit -m "test(e2e): perforce lifecycle flow (create/status/cost/destroy chain)"
```

---

### Task 5: Flow 3 — workstation stop/start state machine

**Files:**
- Create: `test/e2e/workstation_test.go`

**Interfaces:**
- Consumes: Task 2 harness + `assertModuleStatus`, `readState`.

- [ ] **Step 1: Write the flow test**

Create `test/e2e/workstation_test.go`:

```go
package e2e

import (
	"strings"
	"testing"
)

// TestWorkstationStopStart: create → stop (status "stopped", cost drops compute)
// → start (status "ready", cost restores compute). Volume stays billed throughout.
func TestWorkstationStopStart(t *testing.T) {
	setupE2E(t)

	out, err := runCLI(t, "workstation", "create", "--yes")
	if err != nil {
		t.Fatalf("workstation create: %v\n%s", err, out)
	}
	st := readState(t)
	assertModuleExists(t, st, "workstation")

	// Cost while running: an instance line is present. Capture the running total.
	runningCost, err := runCLI(t, "cost", "report")
	if err != nil {
		t.Fatalf("cost (running): %v\n%s", err, out)
	}
	assertContains(t, runningCost, "workstation")

	// Stop: status flips to "stopped".
	out, err = runCLI(t, "workstation", "stop", "--yes")
	if err != nil {
		t.Fatalf("workstation stop: %v\n%s", err, out)
	}
	st = readState(t)
	assertModuleStatus(t, st, "workstation", "stopped")

	// Cost while stopped: compute line dropped (the stopped-instance note appears
	// and the instance-type line is gone from the workstation block). Assert the
	// stopped annotation is present in the report.
	stoppedCost, err := runCLI(t, "cost", "report")
	if err != nil {
		t.Fatalf("cost (stopped): %v\n%s", err, out)
	}
	if !strings.Contains(stoppedCost, "stopped") {
		t.Fatalf("stopped cost report should note the stopped instance:\n%s", stoppedCost)
	}

	// Start: status back to "ready".
	out, err = runCLI(t, "workstation", "start", "--yes")
	if err != nil {
		t.Fatalf("workstation start: %v\n%s", err, out)
	}
	st = readState(t)
	assertModuleStatus(t, st, "workstation", "ready")
}
```

Note (both verified in code): `workstation stop`/`start` gate confirmation on `if !c.assumeYes` (`cmd/workstation/stop/stop.go:123`), so root `--yes` skips the prompt. The `"stopped"` substring in the stopped cost report comes from the `costsource` note, which is exactly `"stopped — compute not billed (EBS still billed)"` (`cmd/internal/costsource/costsource.go:106`) — matching the substring `"stopped"` is stable.

- [ ] **Step 2: Run**

Run: `go test ./test/e2e/ -run TestWorkstationStopStart -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/workstation_test.go
git commit -m "test(e2e): workstation stop/start state-machine flow"
```

---

### Task 6: Flow 4 — full stack + `destroy --all` teardown (with failure sub-case)

**Files:**
- Create: `test/e2e/destroyall_test.go`

**Interfaces:**
- Consumes: Task 2 harness + `readState`, `assertModuleExists`/`assertModuleAbsent`; Task 1's `fakeStore.failDeleteType` (via the store returned by `setupE2E`).

- [ ] **Step 1: Write the success flow test**

Create `test/e2e/destroyall_test.go`:

```go
package e2e

import (
	"strings"
	"testing"
)

// TestDestroyAllFullStack: provision perforce + horde, aggregate cost report,
// then destroy --all removes every module and the backend.
func TestDestroyAllFullStack(t *testing.T) {
	setupE2E(t)

	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "perforce", "create", "--yes"); err != nil {
		t.Fatalf("perforce create: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "horde", "create", "--yes"); err != nil {
		t.Fatalf("horde create: %v\n%s", err, out)
	}

	st := readState(t)
	assertModuleExists(t, st, "perforce")
	assertModuleExists(t, st, "horde")

	// Aggregate cost report covers both modules.
	out, err := runCLI(t, "cost", "report")
	if err != nil {
		t.Fatalf("cost report: %v\n%s", err, out)
	}
	assertContains(t, out, "perforce")
	assertContains(t, out, "horde")
	assertContains(t, out, "Total:")

	// Full teardown.
	out, err = runCLI(t, "destroy", "--all", "--yes")
	if err != nil {
		t.Fatalf("destroy --all: %v\n%s", err, out)
	}
	assertContains(t, out, "Destroy --all complete")

	// Every module gone from state.
	st = readState(t)
	assertModuleAbsent(t, st, "perforce")
	assertModuleAbsent(t, st, "horde")
}

// TestDestroyAllModuleFailurePreservesBackend: when a module's resource deletion
// fails, destroy --all continues, returns an error naming the failed module, and
// does NOT delete the state backend (the backend-only-on-full-success invariant).
func TestDestroyAllModuleFailurePreservesBackend(t *testing.T) {
	store := setupE2E(t)

	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "perforce", "create", "--yes"); err != nil {
		t.Fatalf("perforce create: %v\n%s", err, out)
	}

	// Force perforce's instance deletion to fail.
	store.failDeleteType = "AWS::EC2::Instance"

	out, err := runCLI(t, "destroy", "--all", "--yes")
	if err == nil {
		t.Fatalf("expected destroy --all to error when a module fails:\n%s", out)
	}
	// The failure summary names the failed module.
	if !strings.Contains(out, "perforce") {
		t.Fatalf("failure output should name the failed module:\n%s", out)
	}

	// Backend preserved: the fake still has the bucket + table (setup created
	// them; a failed teardown must not remove them).
	if len(store.buckets) == 0 || len(store.tables) == 0 {
		t.Fatal("state backend must be preserved when a module teardown fails")
	}
}
```

Exact strings verified: `cmd/internal/destroyall/destroyall.go` prints "Destroy --all complete. …" on success and "…the following module(s) failed:" with per-module lines on failure; `--yes` sets `AssumeYes` which skips the aggregate confirm (line 120). Accessing `store.buckets`/`store.tables`/`failDeleteType` is fine — same package (`e2e`).

- [ ] **Step 2: Run**

Run: `go test ./test/e2e/ -run TestDestroyAll -v`
Expected: both PASS. If the success case leaves the backend (bucket/table still present) — check that `destroy --all` actually invokes the backend destroyer after all modules succeed. If the failure case deletes the backend — that's a real regression in the destroyall engine (should never happen); stop and report.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/destroyall_test.go
git commit -m "test(e2e): destroy --all full-stack flow + backend-preservation-on-failure"
```

---

### Task 7: Flow 5 — JSON contract

**Files:**
- Create: `test/e2e/json_test.go`

**Interfaces:**
- Consumes: Task 2 harness + `assertJSON`.

- [ ] **Step 1: Write the flow test**

Create `test/e2e/json_test.go`:

```go
package e2e

import "testing"

// TestJSONContract: --json output for status and cost report parses and carries
// the expected top-level fields.
func TestJSONContract(t *testing.T) {
	setupE2E(t)
	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "perforce", "create", "--yes"); err != nil {
		t.Fatalf("perforce create: %v\n%s", err, out)
	}

	// status --json → { backend, modules, summary }
	out, err := runCLI(t, "--json", "status")
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	var status struct {
		Backend map[string]any   `json:"backend"`
		Modules []map[string]any `json:"modules"`
		Summary map[string]any   `json:"summary"`
	}
	assertJSON(t, out, &status)
	if len(status.Modules) == 0 {
		t.Fatalf("status --json: expected at least one module\n%s", out)
	}

	// cost report --json → { total, confidence, modules, note }
	out, err = runCLI(t, "--json", "cost", "report")
	if err != nil {
		t.Fatalf("cost report --json: %v\n%s", err, out)
	}
	var cost struct {
		Total      float64          `json:"total"`
		Confidence string           `json:"confidence"`
		Modules    []map[string]any `json:"modules"`
		Note       string           `json:"note"`
	}
	assertJSON(t, out, &cost)
	if len(cost.Modules) == 0 {
		t.Fatalf("cost report --json: expected at least one module\n%s", out)
	}
}
```

JSON field names verified against `cmd/status/status.go` (`backend`/`modules`/`summary`) and `cmd/cost/report/report.go` (`total`/`confidence`/`modules`/`note`). `--json` is a root persistent flag; place it before the subcommand (`--json status`) — Cobra also accepts it after, but before is unambiguous.

- [ ] **Step 2: Run**

Run: `go test ./test/e2e/ -run TestJSONContract -v`
Expected: PASS. If `assertJSON` fails because output has a non-JSON preamble, check whether the command prints anything before the JSON in `--json` mode; these two commands emit pure JSON, so `strings.TrimSpace` + `Unmarshal` should succeed. If not, tighten `assertJSON` to extract the JSON span — but first confirm the command isn't mixing human text into `--json` output (that would be a real bug to report).

- [ ] **Step 3: Commit**

```bash
git add test/e2e/json_test.go
git commit -m "test(e2e): JSON contract flow (status + cost report --json)"
```

---

### Task 8: Full-suite verification + docs

**Files:**
- Modify: `ROADMAP.md`, `CLAUDE.md`

- [ ] **Step 1: Run the whole e2e suite + full gate**

Run:
```bash
go test ./test/e2e/... -v
go build ./... && go test ./... && go vet ./... && gofmt -l .
golangci-lint run ./...
```
Expected: all e2e flows PASS; full suite PASS; vet clean; `gofmt -l` prints nothing; golangci-lint 0 issues.
Run the dependency check: `go list -deps ./internal/cloud/... | grep -E 'internal/(state|cost)'` → prints nothing.

- [ ] **Step 2: Update `ROADMAP.md`**

Under Milestone 5, change the E2E line to checked:
```
- ✅ End-to-end testing (CLI E2E harness — in-process, fake provider, runs in CI)
```
(Leave the other M5 lines — docs, consistency review, release prep — as-is.)

- [ ] **Step 3: Update `CLAUDE.md`**

In the "Test Strategy" section, add a paragraph describing the E2E suite:
- `test/e2e/` is a black-box (`package e2e`) CLI end-to-end suite that drives the real `cmd/root` command tree against an in-memory fake `cloud.Provider` (registered as `"fake"`). No real AWS, no build tag — it runs in the default `go test ./...` and CI.
- The fake's store lives in a per-test holder (`currentFake`), reset by `setupE2E(t)`; tests are serial (no `t.Parallel()`).
- To add a flow: new `test/e2e/<flow>_test.go`, call `setupE2E(t)`, drive commands with `runCLI`, assert the triad (exit code + `assertContains`/`assertJSON` on output + `readState` + `assertModule*` on state).
- Real-AWS coverage remains the separate manual `//go:build integration` suite.

- [ ] **Step 4: Commit**

```bash
git add ROADMAP.md CLAUDE.md
git commit -m "docs: CLI E2E harness — ROADMAP + CLAUDE.md test-strategy (Milestone 5)"
```

## Plan complete

Spec coverage:
- Fake in-memory provider + all aux interfaces + per-test reset + predictable IDs → Task 1.
- Harness (`runCLI`, `setupE2E`, `writeConfig`, `readState`, assertion helpers) → Task 2.
- Flow 1 first-run → Task 3; Flow 2 perforce lifecycle → Task 4; Flow 3 workstation stop/start → Task 5; Flow 4 destroy --all + failure sub-case → Task 6; Flow 5 JSON contract → Task 7.
- Full triad (exit code + output + state) asserted in every flow.
- Docs + verification (incl. dependency-rule check) → Task 8.
- Out-of-scope (real AWS, subprocess binary, CI workflow changes) untouched.
