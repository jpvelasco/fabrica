# Deploy Module Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the `fabrica deploy` command family (`setup`, `promote`, `rollback`, `status`, `destroy`) that orchestrates GameLift managed-EC2 fleet deployment of CI/Horde-produced UE5 server builds, with alias-flip blue/green and rollback.

**Architecture:** A pure plan layer (`internal/deploy`, no AWS SDK) builds Cloud Control desired-state JSON for `AWS::IAM::Role`, `AWS::GameLift::Build`, `AWS::GameLift::Fleet`, and `AWS::GameLift::Alias`. Resource CRUD goes through `rt.Provider.Resources()` (Cloud Control). Fleet activation — which takes 20–40 min and the blocking Cloud Control waiter cannot surface — is handled by a new read-only SDK auxiliary interface `cloud.GameLiftManager` (`FleetStatus`/`FleetEvents`) plus a non-blocking fleet-create seam, polled by the `cmd/deploy` layer. This mirrors the existing `CodeBuildRunner`/`EC2InstanceManager` splits.

**Tech Stack:** Go 1.25.11, Cobra, AWS SDK for Go v2 (`gamelift`, `cloudcontrol`), Viper (config), `cost.Global` registry, shared `cmd/internal/teardown` engine.

## Global Constraints

- Go 1.25.11+; module path `github.com/jpvelasco/fabrica`.
- `internal/deploy` imports **no** AWS SDK — pure plan layer (Cloud Control JSON + cost only).
- `internal/cloud` defines provider-agnostic interfaces only — no cloud SDK imports.
- Output via `fmt.Print*` only — no logging library.
- Errors: `fmt.Errorf("context: %w", err)`; messages state what went wrong AND the fix; no sentinel errors (except the existing `cloud.ErrResourceNotFound`).
- Config structs live in `internal/config/config.go` with `mapstructure:` + `yaml:` tags.
- Cost: register each new `TypeName` via `cost.Global.Register`; **never** re-register `AWS::EC2::Instance` or `AWS::EC2::Volume`.
- Tags: Cloud Control desired-state uses capitalized `Tags` array (`[{"Key":...,"Value":...}]`); `injectFabricaTags` merges `ManagedBy: fabrica` into it automatically on Create.
- Test pattern per command: white-box `*_test.go` (`package <cmd>`, injected seams) + black-box `cobra_test.go` (`package <cmd>_test`, minimal root replicating `--dry-run`/`--yes`/`--json` persistent flags on root).
- Coverage target 60%+ for `internal/*`. No real AWS calls in tests.
- `gofmt -w .` + `go vet ./...` clean before every commit. Windows test runs use no `-race`.

## File Structure

**New files:**
- `internal/cloud/gamelift.go` — `GameLiftManager` interface, `FleetInfo`, `FleetEvent`.
- `internal/cloud/aws/gamelift.go` — SDK impl: `FleetStatus`, `FleetEvents`, `CreateFleetAsync`; `gameLiftClient` interface + factory.
- `internal/cloud/aws/gamelift_test.go` — mocked GameLift SDK client tests.
- `internal/deploy/plan.go` — `SetupPlan`, `PromotePlan`, defaults, TypeName consts.
- `internal/deploy/resources.go` — `RoleDesiredState`, `AliasDesiredState`, `BuildDesiredState`, `FleetDesiredState`, `AliasFlipPatch`.
- `internal/deploy/cost.go` — fleet/build/alias estimators + `init()` registration.
- `internal/deploy/plan_test.go`, `resources_test.go`, `cost_test.go`.
- `cmd/deploy/deploy.go` — parent command wiring.
- `cmd/deploy/cobra_test.go` — parent black-box test + shared `cobraFakeProvider`.
- `cmd/deploy/setup/setup.go` + `setup_test.go` + `cobra_test.go`.
- `cmd/deploy/promote/promote.go` + `promote_test.go` + `cobra_test.go`.
- `cmd/deploy/rollback/rollback.go` + `rollback_test.go` + `cobra_test.go`.
- `cmd/deploy/status/status.go` + `status_test.go` + `cobra_test.go`.
- `cmd/deploy/destroy/destroy.go` + `destroy_test.go` + `cobra_test.go`.
- `docs/deploy.md` — module guide.

**Modified files:**
- `internal/config/config.go` — add `DeployConfig` + `Deploy` field + `fileConfig` wiring.
- `internal/cloud/aws/aws.go` — add `gameLiftManager`/client factory fields + `var _` assertion + delegating methods.
- `cmd/internal/teardown/teardown.go` — add `Spec.ResourceOrder` func field; `resourcesToDelete` uses it when non-nil.
- `cmd/root/root.go` — register `deploy.New(...)`.
- `ROADMAP.md`, `CLAUDE.md`, `fabrica.example.yaml` — docs/status updates.

## Task Ordering & Dependencies

Implement in this order. Tasks 1–7 are independent foundation (each compiles and
tests on its own). Tasks 9–13 (subcommands) each depend on Tasks 4–6 (plan layer)
and, for promote/rollback/status, Task 1–2 (GameLiftManager). **Task 8's parent
wiring is finalized in Task 14**, after all five subcommand packages compile —
creating `cmd/deploy/deploy.go` earlier would fail to build (it imports the five
subpackages). Sequence: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 9 → 10 → 11 → 12 → 13 → 14
(14 absorbs the Task 8 parent/root wiring). Task 8 is described separately only
to keep the parent-command spec in one place.

---

### Task 1: `cloud.GameLiftManager` interface

**Files:**
- Create: `internal/cloud/gamelift.go`
- Test: `internal/cloud/gamelift_test.go`

**Interfaces:**
- Consumes: nothing (leaf).
- Produces: `cloud.GameLiftManager` interface with `FleetStatus(ctx, fleetID string) (FleetInfo, error)`, `FleetEvents(ctx, fleetID string) ([]FleetEvent, error)`, `CreateFleetAsync(ctx context.Context, r *Resource) error` (returns once `r.Identifier` is set, without blocking to ACTIVE). Types `FleetInfo{FleetID, Status string}` and `FleetEvent{Code, Message, Time string}`.

- [ ] **Step 1: Write the failing test**

Create `internal/cloud/gamelift_test.go`:

```go
package cloud

import (
	"context"
	"testing"
)

// fakeGLM proves the interface is satisfiable and the types are usable.
type fakeGLM struct{}

func (fakeGLM) FleetStatus(_ context.Context, id string) (FleetInfo, error) {
	return FleetInfo{FleetID: id, Status: "ACTIVE"}, nil
}
func (fakeGLM) FleetEvents(_ context.Context, _ string) ([]FleetEvent, error) {
	return []FleetEvent{{Code: "FLEET_STATE_ACTIVE", Message: "ok", Time: "t"}}, nil
}
func (fakeGLM) CreateFleetAsync(_ context.Context, r *Resource) error {
	r.Identifier = "fleet-123"
	return nil
}

func TestGameLiftManagerInterface(t *testing.T) {
	var m GameLiftManager = fakeGLM{}
	info, err := m.FleetStatus(context.Background(), "fleet-1")
	if err != nil || info.Status != "ACTIVE" {
		t.Fatalf("FleetStatus = %+v, %v", info, err)
	}
	evs, err := m.FleetEvents(context.Background(), "fleet-1")
	if err != nil || len(evs) != 1 || evs[0].Code == "" {
		t.Fatalf("FleetEvents = %+v, %v", evs, err)
	}
	r := &Resource{TypeName: "AWS::GameLift::Fleet"}
	if err := m.CreateFleetAsync(context.Background(), r); err != nil || r.Identifier == "" {
		t.Fatalf("CreateFleetAsync set id=%q err=%v", r.Identifier, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cloud/ -run TestGameLiftManagerInterface`
Expected: FAIL — `undefined: GameLiftManager` / `undefined: FleetInfo`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/cloud/gamelift.go`:

```go
package cloud

import "context"

// GameLiftManager exposes the GameLift operations that the Cloud Control
// ResourceClient cannot: non-blocking fleet creation (Cloud Control's blocking
// Create waits for the fleet to reach ACTIVE, which takes 20–40 minutes and
// hides phase progress and activation-failure detail) and read-only fleet
// status/event queries used to poll activation. Same auxiliary-interface pattern
// as CodeBuildRunner and EC2InstanceManager; reached via type assertion on the
// Provider. All mutations other than CreateFleetAsync go through Cloud Control.
type GameLiftManager interface {
	// CreateFleetAsync fires the fleet create and returns as soon as the fleet
	// identifier (FleetId) is known — it does NOT block until ACTIVE. On return,
	// r.Identifier is populated. Callers poll FleetStatus to track activation.
	CreateFleetAsync(ctx context.Context, r *Resource) error
	// FleetStatus returns the current lifecycle status of a fleet.
	FleetStatus(ctx context.Context, fleetID string) (FleetInfo, error)
	// FleetEvents returns recent fleet events (most-recent first), used to
	// surface the real cause of an activation failure.
	FleetEvents(ctx context.Context, fleetID string) ([]FleetEvent, error)
}

// FleetInfo is the provider-agnostic snapshot of a GameLift fleet.
type FleetInfo struct {
	FleetID string
	// Status is the GameLift fleet status: NEW, DOWNLOADING, VALIDATING,
	// BUILDING, ACTIVATING, ACTIVE, ERROR, DELETING, TERMINATED.
	Status string
}

// FleetEvent is a single GameLift fleet event.
type FleetEvent struct {
	Code    string
	Message string
	Time    string
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cloud/ -run TestGameLiftManagerInterface`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/cloud/gamelift.go internal/cloud/gamelift_test.go
git add internal/cloud/gamelift.go internal/cloud/gamelift_test.go
git commit -m "feat(deploy): add cloud.GameLiftManager auxiliary interface"
```

---

### Task 2: AWS GameLift SDK implementation

**Files:**
- Create: `internal/cloud/aws/gamelift.go`
- Create: `internal/cloud/aws/gamelift_test.go`
- Modify: `internal/cloud/aws/aws.go` (add `newGameLiftClient` factory field)
- Modify: `internal/cloud/aws/cloudcontrol.go` (add `createAsync` helper; ensure `GetResourceRequestStatus` is on `ccAPIClient`)

**Interfaces:**
- Consumes: `cloud.GameLiftManager`, `cloud.FleetInfo`, `cloud.FleetEvent`, `cloud.Resource` (Task 1); existing `awsProvider`, `p.stateBackendConfig(ctx)`, `resourceClients` + `injectFabricaTags` + `progressEventError`.
- Produces: `awsProvider` methods `CreateFleetAsync(ctx, *cloud.Resource) error`, `FleetStatus(ctx, string) (cloud.FleetInfo, error)`, `FleetEvents(ctx, string) ([]cloud.FleetEvent, error)`; `gameLiftClient` interface; `gameLiftClientFactory` + `newGameLiftClient` seam; `resourceClients.createAsync(ctx, *cloud.Resource) error`.

`CreateFleetAsync` calls Cloud Control `CreateResource` (via the existing `resourceClients`) but waits only until `ProgressEvent.Identifier` is set (FleetId is assigned within seconds), not until ACTIVE. Tags injected as in `Create`.

- [ ] **Step 1: Write the failing test**

Create `internal/cloud/aws/gamelift_test.go`:

```go
package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	gltypes "github.com/aws/aws-sdk-go-v2/service/gamelift/types"
)

type fakeGameLiftClient struct {
	describeAttrs  func(context.Context, *gamelift.DescribeFleetAttributesInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error)
	describeEvents func(context.Context, *gamelift.DescribeFleetEventsInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error)
}

func (f fakeGameLiftClient) DescribeFleetAttributes(ctx context.Context, in *gamelift.DescribeFleetAttributesInput, o ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
	return f.describeAttrs(ctx, in, o...)
}
func (f fakeGameLiftClient) DescribeFleetEvents(ctx context.Context, in *gamelift.DescribeFleetEventsInput, o ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error) {
	return f.describeEvents(ctx, in, o...)
}

// newTestProviderGL wires a fake GameLift client and a no-op config loader.
// The config-loader field name must match the one stateBackendConfig uses
// (loadConfig); confirm against internal/cloud/aws/state_backend.go.
func newTestProviderGL(c gameLiftClient) *awsProvider {
	return &awsProvider{
		cfg:               testConfig(),
		awsCfg:            awsConfig{region: "us-east-1"},
		newGameLiftClient: func(aws.Config) gameLiftClient { return c },
		loadConfig:        func(ctx context.Context, region, profile string) (aws.Config, error) { return aws.Config{}, nil },
	}
}

func TestFleetStatus(t *testing.T) {
	c := fakeGameLiftClient{
		describeAttrs: func(_ context.Context, _ *gamelift.DescribeFleetAttributesInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
			return &gamelift.DescribeFleetAttributesOutput{
				FleetAttributes: []gltypes.FleetAttributes{{
					FleetId: aws.String("fleet-1"),
					Status:  gltypes.FleetStatusActive,
				}},
			}, nil
		},
	}
	info, err := newTestProviderGL(c).FleetStatus(context.Background(), "fleet-1")
	if err != nil {
		t.Fatalf("FleetStatus err: %v", err)
	}
	if info.Status != "ACTIVE" || info.FleetID != "fleet-1" {
		t.Fatalf("got %+v", info)
	}
}

func TestFleetStatusNotFound(t *testing.T) {
	c := fakeGameLiftClient{
		describeAttrs: func(_ context.Context, _ *gamelift.DescribeFleetAttributesInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
			return &gamelift.DescribeFleetAttributesOutput{FleetAttributes: nil}, nil
		},
	}
	_, err := newTestProviderGL(c).FleetStatus(context.Background(), "fleet-x")
	if err == nil {
		t.Fatal("expected error for missing fleet")
	}
}

func TestFleetEvents(t *testing.T) {
	c := fakeGameLiftClient{
		describeEvents: func(_ context.Context, _ *gamelift.DescribeFleetEventsInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error) {
			return &gamelift.DescribeFleetEventsOutput{
				Events: []gltypes.Event{{
					EventCode: gltypes.EventCodeFleetStateError,
					Message:   aws.String("bad launch path"),
				}},
			}, nil
		},
	}
	evs, err := newTestProviderGL(c).FleetEvents(context.Background(), "fleet-1")
	if err != nil {
		t.Fatalf("FleetEvents err: %v", err)
	}
	if len(evs) != 1 || evs[0].Message != "bad launch path" {
		t.Fatalf("got %+v", evs)
	}
}

func TestFleetEventsAPIError(t *testing.T) {
	c := fakeGameLiftClient{
		describeEvents: func(_ context.Context, _ *gamelift.DescribeFleetEventsInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error) {
			return nil, errors.New("throttled")
		},
	}
	_, err := newTestProviderGL(c).FleetEvents(context.Background(), "fleet-1")
	if err == nil {
		t.Fatal("expected error propagated")
	}
}
```

**Before writing the test, confirm two existing names** so the test compiles: (a) `testConfig()` helper exists in the `aws` test package (used by `codebuild_test.go`); (b) the config-loader seam field on `awsProvider` — open `internal/cloud/aws/state_backend.go` and use whatever field `stateBackendConfig` reads (`loadConfig stateBackendConfigLoader`). Match `newTestProviderGL` to it.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cloud/aws/ -run TestFleet`
Expected: FAIL — `undefined: gameLiftClient` / `undefined: newGameLiftClient`.

- [ ] **Step 3: Add the GameLift SDK dependency**

Run:
```bash
go get github.com/aws/aws-sdk-go-v2/service/gamelift@latest
go mod tidy
```
Expected: `go.mod` gains the `gamelift` require line.

- [ ] **Step 4: Write minimal implementation**

Create `internal/cloud/aws/gamelift.go`:

```go
package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.GameLiftManager = (*awsProvider)(nil)

// gameLiftClient is the subset of the GameLift SDK the provider uses.
type gameLiftClient interface {
	DescribeFleetAttributes(context.Context, *gamelift.DescribeFleetAttributesInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error)
	DescribeFleetEvents(context.Context, *gamelift.DescribeFleetEventsInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error)
}

type gameLiftClientFactory func(aws.Config) gameLiftClient

func (p *awsProvider) gameLiftClient(cfg aws.Config) gameLiftClient {
	if p.newGameLiftClient != nil {
		return p.newGameLiftClient(cfg)
	}
	return gamelift.NewFromConfig(cfg)
}

// CreateFleetAsync creates the fleet via Cloud Control but returns as soon as the
// FleetId is assigned, without blocking until ACTIVE. The cmd layer polls
// FleetStatus to track activation.
func (p *awsProvider) CreateFleetAsync(ctx context.Context, r *fabricac.Resource) error {
	return p.Resources().(*resourceClients).createAsync(ctx, r)
}

func (p *awsProvider) FleetStatus(ctx context.Context, fleetID string) (fabricac.FleetInfo, error) {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return fabricac.FleetInfo{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := p.gameLiftClient(cfg).DescribeFleetAttributes(ctx, &gamelift.DescribeFleetAttributesInput{
		FleetIds: []string{fleetID},
	})
	if err != nil {
		return fabricac.FleetInfo{}, fmt.Errorf("describing fleet %s: %w", fleetID, err)
	}
	if len(out.FleetAttributes) == 0 {
		return fabricac.FleetInfo{}, fmt.Errorf("fleet %s not found — check 'fabrica deploy status'", fleetID)
	}
	a := out.FleetAttributes[0]
	return fabricac.FleetInfo{FleetID: aws.ToString(a.FleetId), Status: string(a.Status)}, nil
}

func (p *awsProvider) FleetEvents(ctx context.Context, fleetID string) ([]fabricac.FleetEvent, error) {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := p.gameLiftClient(cfg).DescribeFleetEvents(ctx, &gamelift.DescribeFleetEventsInput{
		FleetId: aws.String(fleetID),
		Limit:   aws.Int32(20),
	})
	if err != nil {
		return nil, fmt.Errorf("describing events for fleet %s: %w", fleetID, err)
	}
	events := make([]fabricac.FleetEvent, 0, len(out.Events))
	for _, e := range out.Events {
		ev := fabricac.FleetEvent{Code: string(e.EventCode), Message: aws.ToString(e.Message)}
		if e.EventTime != nil {
			ev.Time = e.EventTime.Format(time.RFC3339)
		}
		events = append(events, ev)
	}
	return events, nil
}
```

Add to the `awsProvider` struct in `internal/cloud/aws/aws.go` (next to `newCodeBuildClient`):

```go
	newGameLiftClient gameLiftClientFactory
```

Add `createAsync` to `internal/cloud/aws/cloudcontrol.go` (the file already imports `aws`, `cloudcontrol`, `types`, `time`, `fmt`):

```go
// createAsync fires CreateResource and returns once the resource Identifier is
// known, WITHOUT waiting for the resource to stabilize. Used for GameLift fleets,
// whose activation (20–40 min) the blocking Create() waiter cannot surface.
func (c *resourceClients) createAsync(ctx context.Context, r *fabricac.Resource) error {
	if err := c.ensureClient(ctx); err != nil {
		return err
	}
	r.DesiredState = injectFabricaTags(r.DesiredState, "fabrica", c.version, nil)

	out, err := c.cc.CreateResource(ctx, &cloudcontrol.CreateResourceInput{
		TypeName:     aws.String(r.TypeName),
		DesiredState: aws.String(string(r.DesiredState)),
	})
	if err != nil {
		return fmt.Errorf("creating %s: %w", r.TypeName, err)
	}
	if id := aws.ToString(out.ProgressEvent.Identifier); id != "" {
		r.Identifier = id
		return nil
	}
	token := aws.ToString(out.ProgressEvent.RequestToken)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		st, err := c.cc.GetResourceRequestStatus(ctx, &cloudcontrol.GetResourceRequestStatusInput{
			RequestToken: aws.String(token),
		})
		if err != nil {
			return fmt.Errorf("polling %s create request: %w", r.TypeName, err)
		}
		ev := st.ProgressEvent
		if ev.OperationStatus == types.OperationStatusFailed {
			return progressEventError(r.TypeName, ev)
		}
		if id := aws.ToString(ev.Identifier); id != "" {
			r.Identifier = id
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for %s identifier (60s) — check the AWS console and retry", r.TypeName)
}
```

**Confirm `ccAPIClient` has `GetResourceRequestStatus`.** Find the `ccAPIClient` interface (in `aws.go` or `cloudcontrol.go`). If it lacks the method, add:
```go
	GetResourceRequestStatus(context.Context, *cloudcontrol.GetResourceRequestStatusInput, ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceRequestStatusOutput, error)
```
(The real `*cloudcontrol.Client` already implements it; existing fakes that implement `ccAPIClient` in `cloudcontrol_test.go` may need a stub method added — search for them and add a no-op returning `nil, nil` if any fail to compile.)

- [ ] **Step 5: Run tests + build**

Run: `go test ./internal/cloud/aws/ -run TestFleet` → PASS (4 tests).
Run: `go build ./...` → succeeds.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/cloud/aws/
git add internal/cloud/aws/gamelift.go internal/cloud/aws/gamelift_test.go internal/cloud/aws/cloudcontrol.go internal/cloud/aws/aws.go go.mod go.sum
git commit -m "feat(deploy): implement GameLiftManager (FleetStatus/Events + async fleet create)"
```

---

### Task 3: `DeployConfig` in config

**Files:**
- Modify: `internal/config/config.go` (add struct, `Config.Deploy` field, `fileConfig` field + mapping)
- Test: `internal/config/config_test.go` (add a case to the existing load test, or a new `TestLoadDeployConfig`)

**Interfaces:**
- Consumes: existing `Config`, `fileConfig` structs.
- Produces: `config.DeployConfig{ AliasName, RoleName, FleetName, InstanceType, FleetType, LaunchPath, BuildBucket, BuildOS string; FromPort, ToPort, ActivationTimeoutMinutes, DesiredInstances int }` accessible as `cfg.Deploy`.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestLoadDeployConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	if err := os.WriteFile(path, []byte(`
cloud:
  provider: aws
deploy:
  instanceType: c5.large
  fleetType: ON_DEMAND
  launchPath: /local/game/ServerApp
  buildBucket: my-build-bucket
  desiredInstances: 2
  activationTimeoutMinutes: 30
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Deploy.InstanceType != "c5.large" {
		t.Errorf("InstanceType = %q", cfg.Deploy.InstanceType)
	}
	if cfg.Deploy.DesiredInstances != 2 {
		t.Errorf("DesiredInstances = %d", cfg.Deploy.DesiredInstances)
	}
	if cfg.Deploy.ActivationTimeoutMinutes != 30 {
		t.Errorf("ActivationTimeoutMinutes = %d", cfg.Deploy.ActivationTimeoutMinutes)
	}
}
```

(Ensure `path/filepath` and `os` are imported in the test file — they already are if other load tests exist.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadDeployConfig`
Expected: FAIL — `cfg.Deploy undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/config/config.go`, add the field to `Config` (after `CI`):

```go
	Deploy      DeployConfig      `mapstructure:"deploy"      yaml:"deploy"`
```

Add the field to `fileConfig` (after `CI`):

```go
	Deploy      DeployConfig      `yaml:"deploy"`
```

Map it in `fileConfig()` (after `CI: c.CI,`):

```go
		Deploy:      c.Deploy,
```

Add the struct (after `CIConfig`):

```go
// DeployConfig holds the deploy: section of fabrica.yaml. Defaults are applied
// in the deploy plan layer (internal/deploy), matching the CI/Horde pattern.
type DeployConfig struct {
	AliasName                string `mapstructure:"aliasName"                yaml:"aliasName"`
	RoleName                 string `mapstructure:"roleName"                 yaml:"roleName"`
	FleetName                string `mapstructure:"fleetName"                yaml:"fleetName"`
	InstanceType             string `mapstructure:"instanceType"             yaml:"instanceType"`
	FleetType                string `mapstructure:"fleetType"                yaml:"fleetType"`
	LaunchPath               string `mapstructure:"launchPath"               yaml:"launchPath"`
	BuildBucket              string `mapstructure:"buildBucket"              yaml:"buildBucket"`
	BuildOS                  string `mapstructure:"buildOs"                  yaml:"buildOs"`
	FromPort                 int    `mapstructure:"fromPort"                 yaml:"fromPort"`
	ToPort                   int    `mapstructure:"toPort"                   yaml:"toPort"`
	DesiredInstances         int    `mapstructure:"desiredInstances"         yaml:"desiredInstances"`
	ActivationTimeoutMinutes int    `mapstructure:"activationTimeoutMinutes" yaml:"activationTimeoutMinutes"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestLoadDeployConfig`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/config/config.go internal/config/config_test.go
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(deploy): add DeployConfig to config schema"
```

---

### Task 4: `internal/deploy` plan layer

**Files:**
- Create: `internal/deploy/plan.go`
- Test: `internal/deploy/plan_test.go`

**Interfaces:**
- Consumes: `config.DeployConfig` (Task 3); `cost.Resource`.
- Produces:
  - TypeName consts: `TypeAWSIAMRole = "AWS::IAM::Role"`, `TypeGameLiftAlias = "AWS::GameLift::Alias"`, `TypeGameLiftBuild = "AWS::GameLift::Build"`, `TypeGameLiftFleet = "AWS::GameLift::Fleet"`.
  - `SetupPlan{ Account, Region, RoleName, AliasName, BuildBucket string; CostResources []cost.Resource }`.
  - `PromotePlan{ Account, Region, BuildVersion, RoleName, RoleARN, AliasID, FleetName, BuildName, InstanceType, FleetType, LaunchPath, BuildOS, S3Bucket, S3Key string; FromPort, ToPort, DesiredInstances, ActivationTimeoutMinutes int; CostResources []cost.Resource }`.
  - `NewSetupPlan(cfg config.DeployConfig, account, region string) *SetupPlan`.
  - `NewPromotePlan(cfg config.DeployConfig, account, region, buildVersion, roleARN, aliasID, s3Bucket, s3Key string) *PromotePlan`.
  - `fleetCostName(instanceType string, desired int) string` returning `"<instanceType>x<desired>"`.

- [ ] **Step 1: Write the failing test**

Create `internal/deploy/plan_test.go`:

```go
package deploy

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewSetupPlanDefaults(t *testing.T) {
	p := NewSetupPlan(config.DeployConfig{}, "123456789012", "us-east-1")
	if p.RoleName != defaultRoleName {
		t.Errorf("RoleName = %q, want %q", p.RoleName, defaultRoleName)
	}
	if p.AliasName != defaultAliasName {
		t.Errorf("AliasName = %q, want %q", p.AliasName, defaultAliasName)
	}
}

func TestNewSetupPlanOverrides(t *testing.T) {
	p := NewSetupPlan(config.DeployConfig{RoleName: "my-role", AliasName: "my-alias"}, "123456789012", "us-east-1")
	if p.RoleName != "my-role" || p.AliasName != "my-alias" {
		t.Errorf("overrides not applied: %+v", p)
	}
}

func TestNewPromotePlanDefaultsAndS3(t *testing.T) {
	p := NewPromotePlan(config.DeployConfig{BuildBucket: "bkt"}, "123456789012", "us-east-1",
		"v1.2.3", "arn:aws:iam::123456789012:role/fabrica-deploy", "alias-1", "", "")
	if p.InstanceType != defaultInstanceType {
		t.Errorf("InstanceType = %q, want %q", p.InstanceType, defaultInstanceType)
	}
	if p.FleetType != defaultFleetType {
		t.Errorf("FleetType = %q, want %q", p.FleetType, defaultFleetType)
	}
	if p.DesiredInstances != defaultDesiredInstances {
		t.Errorf("DesiredInstances = %d", p.DesiredInstances)
	}
	if p.ActivationTimeoutMinutes != defaultActivationTimeoutMinutes {
		t.Errorf("ActivationTimeoutMinutes = %d", p.ActivationTimeoutMinutes)
	}
	// S3 defaults: bucket from config, key from build-version convention.
	if p.S3Bucket != "bkt" {
		t.Errorf("S3Bucket = %q", p.S3Bucket)
	}
	if p.S3Key != "builds/v1.2.3/server.zip" {
		t.Errorf("S3Key = %q", p.S3Key)
	}
	// Fleet/build names incorporate the sanitized build version.
	if p.FleetName == "" || p.BuildName == "" {
		t.Errorf("names empty: %+v", p)
	}
	// Cost resource encodes instance type + count.
	if len(p.CostResources) == 0 || p.CostResources[0].TypeName != TypeGameLiftFleet {
		t.Errorf("CostResources = %+v", p.CostResources)
	}
}

func TestNewPromotePlanExplicitS3(t *testing.T) {
	p := NewPromotePlan(config.DeployConfig{}, "123456789012", "us-east-1",
		"v1", "arn:role", "alias-1", "other-bucket", "custom/key.zip")
	if p.S3Bucket != "other-bucket" || p.S3Key != "custom/key.zip" {
		t.Errorf("explicit S3 not honored: %+v", p)
	}
}

func TestFleetCostName(t *testing.T) {
	if got := fleetCostName("c5.large", 2); got != "c5.largex2" {
		t.Errorf("fleetCostName = %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deploy/ -run TestNew`
Expected: FAIL — package/identifiers undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/deploy/plan.go`:

```go
// Package deploy is the pure plan layer for the Fabrica deploy module. It builds
// the setup/promote plans and Cloud Control desired-state JSON for the GameLift
// IAM role, alias, build, and fleet that deploy a game-server build. It imports
// no AWS SDK — the cmd/deploy layer executes plans via rt.Provider.Resources()
// and the cloud.GameLiftManager auxiliary interface.
package deploy

import (
	"fmt"
	"strings"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

const (
	TypeAWSIAMRole    = "AWS::IAM::Role"
	TypeGameLiftAlias = "AWS::GameLift::Alias"
	TypeGameLiftBuild = "AWS::GameLift::Build"
	TypeGameLiftFleet = "AWS::GameLift::Fleet"

	defaultRoleName                 = "fabrica-deploy-gamelift"
	defaultAliasName                = "fabrica-deploy"
	defaultFleetPrefix              = "fabrica-fleet"
	defaultBuildPrefix              = "fabrica-build"
	defaultInstanceType             = "c5.large"
	defaultFleetType                = "ON_DEMAND"
	defaultBuildOS                  = "AMAZON_LINUX_2"
	defaultLaunchPath               = "/local/game/ServerApp"
	defaultFromPort                 = 7777
	defaultToPort                   = 7777
	defaultDesiredInstances         = 1
	defaultActivationTimeoutMinutes = 45
)

// SetupPlan is the resolved deploy setup plan (IAM role + alias).
type SetupPlan struct {
	Account       string
	Region        string
	RoleName      string
	AliasName     string
	BuildBucket   string
	CostResources []cost.Resource
}

// PromotePlan is the resolved promote plan (build registration + new fleet).
type PromotePlan struct {
	Account                  string
	Region                   string
	BuildVersion             string
	RoleName                 string
	RoleARN                  string
	AliasID                  string
	FleetName                string
	BuildName                string
	InstanceType             string
	FleetType                string
	LaunchPath               string
	BuildOS                  string
	S3Bucket                 string
	S3Key                    string
	FromPort                 int
	ToPort                   int
	DesiredInstances         int
	ActivationTimeoutMinutes int
	CostResources            []cost.Resource
}

// NewSetupPlan builds the setup plan, applying defaults for unset config fields.
func NewSetupPlan(cfg config.DeployConfig, account, region string) *SetupPlan {
	roleName := cfg.RoleName
	if roleName == "" {
		roleName = defaultRoleName
	}
	aliasName := cfg.AliasName
	if aliasName == "" {
		aliasName = defaultAliasName
	}
	return &SetupPlan{
		Account:     account,
		Region:      region,
		RoleName:    roleName,
		AliasName:   aliasName,
		BuildBucket: cfg.BuildBucket,
		CostResources: []cost.Resource{
			{TypeName: TypeAWSIAMRole, Name: roleName},
			{TypeName: TypeGameLiftAlias, Name: aliasName},
		},
	}
}

// NewPromotePlan builds the promote plan. s3Bucket/s3Key override the convention
// (cfg.BuildBucket + "builds/<version>/server.zip") when non-empty.
func NewPromotePlan(cfg config.DeployConfig, account, region, buildVersion, roleARN, aliasID, s3Bucket, s3Key string) *PromotePlan {
	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = defaultInstanceType
	}
	fleetType := cfg.FleetType
	if fleetType == "" {
		fleetType = defaultFleetType
	}
	launchPath := cfg.LaunchPath
	if launchPath == "" {
		launchPath = defaultLaunchPath
	}
	buildOS := cfg.BuildOS
	if buildOS == "" {
		buildOS = defaultBuildOS
	}
	fromPort := cfg.FromPort
	if fromPort == 0 {
		fromPort = defaultFromPort
	}
	toPort := cfg.ToPort
	if toPort == 0 {
		toPort = defaultToPort
	}
	desired := cfg.DesiredInstances
	if desired <= 0 {
		desired = defaultDesiredInstances
	}
	timeout := cfg.ActivationTimeoutMinutes
	if timeout <= 0 {
		timeout = defaultActivationTimeoutMinutes
	}
	if s3Bucket == "" {
		s3Bucket = cfg.BuildBucket
	}
	if s3Key == "" {
		s3Key = fmt.Sprintf("builds/%s/server.zip", buildVersion)
	}
	slug := sanitize(buildVersion)
	roleName := cfg.RoleName
	if roleName == "" {
		roleName = defaultRoleName
	}
	return &PromotePlan{
		Account:                  account,
		Region:                   region,
		BuildVersion:             buildVersion,
		RoleName:                 roleName,
		RoleARN:                  roleARN,
		AliasID:                  aliasID,
		FleetName:                fmt.Sprintf("%s-%s", defaultFleetPrefix, slug),
		BuildName:                fmt.Sprintf("%s-%s", defaultBuildPrefix, slug),
		InstanceType:             instanceType,
		FleetType:                fleetType,
		LaunchPath:               launchPath,
		BuildOS:                  buildOS,
		S3Bucket:                 s3Bucket,
		S3Key:                    s3Key,
		FromPort:                 fromPort,
		ToPort:                   toPort,
		DesiredInstances:         desired,
		ActivationTimeoutMinutes: timeout,
		CostResources: []cost.Resource{
			{TypeName: TypeGameLiftFleet, Name: fleetCostName(instanceType, desired)},
			{TypeName: TypeGameLiftBuild, Name: buildVersion},
		},
	}
}

// fleetCostName encodes the instance type and desired count for the cost
// estimator to parse (mirrors the "gp3-<n>GiB" convention in perforce/cost.go).
func fleetCostName(instanceType string, desired int) string {
	return fmt.Sprintf("%sx%d", instanceType, desired)
}

// sanitize lowercases and replaces characters invalid in GameLift names/IDs.
func sanitize(s string) string {
	s = strings.ToLower(s)
	repl := func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}
	return strings.Map(repl, s)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/deploy/ -run TestNew && go test ./internal/deploy/ -run TestFleetCostName`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/deploy/
git add internal/deploy/plan.go internal/deploy/plan_test.go
git commit -m "feat(deploy): add deploy plan layer (SetupPlan/PromotePlan)"
```

---

### Task 5: `internal/deploy` Cloud Control desired-state builders

**Files:**
- Create: `internal/deploy/resources.go`
- Test: `internal/deploy/resources_test.go`

**Interfaces:**
- Consumes: `SetupPlan`, `PromotePlan` (Task 4).
- Produces:
  - `RoleDesiredState(plan *SetupPlan) (json.RawMessage, error)` — IAM role, trust `gamelift.amazonaws.com`, inline `s3:GetObject` on `arn:aws:s3:::<BuildBucket>/*`.
  - `AliasDesiredState(plan *SetupPlan) (json.RawMessage, error)` — alias with `TERMINAL` routing (`MESSAGE`) placeholder.
  - `BuildDesiredState(plan *PromotePlan) (json.RawMessage, error)` — `StorageLocation` (bucket/key/role) + `OperatingSystem` + `Version`.
  - `FleetDesiredState(plan *PromotePlan, buildID string) (json.RawMessage, error)` — EC2 fleet referencing the build.
  - `AliasFlipPatch(fleetID string) (json.RawMessage, error)` — RFC-6902 patch repointing `RoutingStrategy` to `SIMPLE`/`FleetId`.

- [ ] **Step 1: Write the failing test**

Create `internal/deploy/resources_test.go`:

```go
package deploy

import (
	"encoding/json"
	"strings"
	"testing"
)

func setupPlanFixture() *SetupPlan {
	return &SetupPlan{Account: "123456789012", Region: "us-east-1", RoleName: "r", AliasName: "a", BuildBucket: "bkt"}
}
func promotePlanFixture() *PromotePlan {
	return &PromotePlan{
		Account: "123456789012", Region: "us-east-1", BuildVersion: "v1",
		RoleARN: "arn:aws:iam::123456789012:role/r", AliasID: "alias-1",
		FleetName: "fabrica-fleet-v1", BuildName: "fabrica-build-v1",
		InstanceType: "c5.large", FleetType: "ON_DEMAND", LaunchPath: "/local/game/ServerApp",
		BuildOS: "AMAZON_LINUX_2", S3Bucket: "bkt", S3Key: "builds/v1/server.zip",
		FromPort: 7777, ToPort: 7777, DesiredInstances: 2,
	}
}

func TestRoleDesiredState(t *testing.T) {
	raw, err := RoleDesiredState(setupPlanFixture())
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "gamelift.amazonaws.com") {
		t.Error("missing gamelift trust principal")
	}
	if !strings.Contains(s, "arn:aws:s3:::bkt/*") {
		t.Error("missing scoped s3 resource")
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestAliasDesiredStateTerminal(t *testing.T) {
	raw, err := AliasDesiredState(setupPlanFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "TERMINAL") {
		t.Error("setup alias should use TERMINAL routing placeholder")
	}
}

func TestBuildDesiredState(t *testing.T) {
	raw, err := BuildDesiredState(promotePlanFixture())
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"builds/v1/server.zip", "AMAZON_LINUX_2", "\"Version\":\"v1\""} {
		if !strings.Contains(s, want) {
			t.Errorf("build state missing %q in %s", want, s)
		}
	}
}

func TestFleetDesiredState(t *testing.T) {
	raw, err := FleetDesiredState(promotePlanFixture(), "build-123")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"build-123", "c5.large", "EC2", "ON_DEMAND", "/local/game/ServerApp"} {
		if !strings.Contains(s, want) {
			t.Errorf("fleet state missing %q", want)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestAliasFlipPatch(t *testing.T) {
	raw, err := AliasFlipPatch("fleet-999")
	if err != nil {
		t.Fatal(err)
	}
	var patch []map[string]any
	if err := json.Unmarshal(raw, &patch); err != nil {
		t.Fatalf("patch must be a JSON array: %v", err)
	}
	if !strings.Contains(string(raw), "fleet-999") || !strings.Contains(string(raw), "SIMPLE") {
		t.Errorf("patch missing fleet id or SIMPLE routing: %s", raw)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deploy/ -run 'TestRole|TestAlias|TestBuild|TestFleetDesired'`
Expected: FAIL — builders undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/deploy/resources.go`:

```go
package deploy

import (
	"encoding/json"
	"fmt"
)

// RoleDesiredState returns the Cloud Control desired-state JSON for the IAM role
// GameLift assumes to read the build from S3.
func RoleDesiredState(plan *SetupPlan) (json.RawMessage, error) {
	bucketArn := fmt.Sprintf("arn:aws:s3:::%s/*", plan.BuildBucket)
	doc := map[string]any{
		"RoleName": plan.RoleName,
		"AssumeRolePolicyDocument": map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{{
				"Effect":    "Allow",
				"Principal": map[string]any{"Service": "gamelift.amazonaws.com"},
				"Action":    "sts:AssumeRole",
			}},
		},
		"Policies": []map[string]any{{
			"PolicyName": "fabrica-deploy-s3-read",
			"PolicyDocument": map[string]any{
				"Version": "2012-10-17",
				"Statement": []map[string]any{{
					"Effect":   "Allow",
					"Action":   []string{"s3:GetObject"},
					"Resource": bucketArn,
				}},
			},
		}},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.RoleName},
		},
	}
	return json.Marshal(doc)
}

// AliasDesiredState returns the desired state for the setup alias. Until the
// first promote there is no fleet, so the alias uses TERMINAL routing with a
// MESSAGE — valid and resolvable, just not pointing at a fleet yet.
func AliasDesiredState(plan *SetupPlan) (json.RawMessage, error) {
	doc := map[string]any{
		"Name": plan.AliasName,
		"RoutingStrategy": map[string]any{
			"Type":    "TERMINAL",
			"Message": "Fabrica deploy alias — run 'fabrica deploy promote <build-version>' to point this at a fleet.",
		},
	}
	return json.Marshal(doc)
}

// BuildDesiredState returns the desired state for the GameLift build that
// references the packaged server in S3.
func BuildDesiredState(plan *PromotePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"Name":            plan.BuildName,
		"Version":         plan.BuildVersion,
		"OperatingSystem": plan.BuildOS,
		"StorageLocation": map[string]any{
			"Bucket":  plan.S3Bucket,
			"Key":     plan.S3Key,
			"RoleArn": plan.RoleARN,
		},
	}
	return json.Marshal(doc)
}

// FleetDesiredState returns the desired state for a managed EC2 fleet running
// the given build.
func FleetDesiredState(plan *PromotePlan, buildID string) (json.RawMessage, error) {
	doc := map[string]any{
		"Name":            plan.FleetName,
		"BuildId":         buildID,
		"ComputeType":     "EC2",
		"EC2InstanceType": plan.InstanceType,
		"FleetType":       plan.FleetType,
		"CertificateConfiguration": map[string]any{
			"CertificateType": "DISABLED",
		},
		"EC2InboundPermissions": []map[string]any{{
			"FromPort": plan.FromPort,
			"ToPort":   plan.ToPort,
			"IpRange":  "0.0.0.0/0",
			"Protocol": "UDP",
		}},
		"RuntimeConfiguration": map[string]any{
			"ServerProcesses": []map[string]any{{
				"ConcurrentExecutions": 1,
				"LaunchPath":           plan.LaunchPath,
			}},
		},
		"Locations": []map[string]any{
			{"Location": plan.Region},
		},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.FleetName},
			{"Key": "BuildVersion", "Value": plan.BuildVersion},
		},
	}
	return json.Marshal(doc)
}

// AliasFlipPatch returns an RFC-6902 patch document that repoints an alias's
// routing strategy to SIMPLE/<fleetID>. Applied via ResourceClient.Update.
func AliasFlipPatch(fleetID string) (json.RawMessage, error) {
	patch := []map[string]any{{
		"op":   "replace",
		"path": "/RoutingStrategy",
		"value": map[string]any{
			"Type":    "SIMPLE",
			"FleetId": fleetID,
		},
	}}
	return json.Marshal(patch)
}
```

**Note on capacity:** `DesiredEC2Instances` is intentionally omitted from `FleetDesiredState` (it is not a top-level Cloud Control fleet property in all schema versions; default capacity is 0–1 and capacity scaling is a Classis concern per the spec). `DesiredInstances` from the plan is used only for the cost estimate. If integration testing shows the fleet needs explicit desired capacity, that is a follow-up — out of V1 scope.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/deploy/ -run 'TestRole|TestAlias|TestBuild|TestFleetDesired'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/deploy/
git add internal/deploy/resources.go internal/deploy/resources_test.go
git commit -m "feat(deploy): add GameLift Cloud Control desired-state builders"
```

---

### Task 6: `internal/deploy` cost estimators

**Files:**
- Create: `internal/deploy/cost.go`
- Test: `internal/deploy/cost_test.go`

**Interfaces:**
- Consumes: `cost.Resource`, `cost.Monthly`, `cost.Global` (existing); `TypeGameLiftFleet`/`TypeGameLiftBuild`/`TypeGameLiftAlias` + `fleetCostName` (Task 4); the `ec2InstancePrices` table lives in `internal/perforce` and is **not** exported — the fleet estimator carries its own small GameLift price table (GameLift EC2 on-demand ≈ EC2 on-demand for the same family).
- Produces: `init()` registering estimators for `TypeGameLiftFleet`, `TypeGameLiftBuild`, `TypeGameLiftAlias`. **Does not** register `AWS::IAM::Role` (already registered by `internal/ci/cost.go`) or `AWS::EC2::Instance`.

**Important — avoid duplicate registration panic:** `cost.Global.Register` panics on a duplicate `TypeName`. `internal/ci/cost.go` already registers `AWS::IAM::Role`. The deploy module also provisions an IAM role, but it must **not** re-register that type. The setup cost preview reuses the existing `AWS::IAM::Role` estimator automatically via the registry. Only register the three GameLift types here.

- [ ] **Step 1: Write the failing test**

Create `internal/deploy/cost_test.go`:

```go
package deploy

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/cost"
)

func TestFleetEstimator(t *testing.T) {
	m, err := cost.Global.Estimate(TypeGameLiftFleet, cost.Resource{TypeName: TypeGameLiftFleet, Name: fleetCostName("c5.large", 2)})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	// c5.large ~ $0.085/hr * 730 * 2 instances ~= $124.10
	if m.Amount < 100 || m.Amount > 150 {
		t.Errorf("fleet monthly = %.2f, expected ~124", m.Amount)
	}
}

func TestFleetEstimatorUnknownType(t *testing.T) {
	_, err := cost.Global.Estimate(TypeGameLiftFleet, cost.Resource{TypeName: TypeGameLiftFleet, Name: "wat.huge x9"})
	if err == nil {
		t.Error("expected error for unparseable fleet cost name")
	}
}

func TestBuildAndAliasFree(t *testing.T) {
	for _, tn := range []string{TypeGameLiftBuild, TypeGameLiftAlias} {
		m, err := cost.Global.Estimate(tn, cost.Resource{TypeName: tn, Name: "x"})
		if err != nil {
			t.Fatalf("%s: %v", tn, err)
		}
		if m.Amount != 0 {
			t.Errorf("%s should be free, got %.2f", tn, m.Amount)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deploy/ -run 'TestFleetEstimator|TestBuildAndAlias'`
Expected: FAIL — no estimator registered for the GameLift types.

- [ ] **Step 3: Write minimal implementation**

Create `internal/deploy/cost.go`:

```go
package deploy

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jpvelasco/fabrica/internal/cost"
)

const gameLiftHoursPerMonth = 730.0

// gameLiftInstancePrices is a small on-demand price table for GameLift-hosted
// EC2 instances (us-east-1, Linux, on-demand). GameLift EC2 pricing tracks EC2
// on-demand closely for these families. Low/Medium confidence by nature.
var gameLiftInstancePrices = map[string]float64{
	"c5.large":   0.085,
	"c5.xlarge":  0.170,
	"c5.2xlarge": 0.340,
	"c5.4xlarge": 0.680,
	"c4.large":   0.100,
	"c4.xlarge":  0.199,
	"m5.large":   0.096,
	"m5.xlarge":  0.192,
	"m5.2xlarge": 0.384,
	"r5.large":   0.126,
	"r5.xlarge":  0.252,
}

// fleetEstimator parses a "<instanceType>x<count>" name (see fleetCostName) and
// multiplies the hourly rate by the instance count and hours/month.
type fleetEstimator struct{}

func (fleetEstimator) Estimate(r cost.Resource) (cost.Monthly, error) {
	idx := strings.LastIndex(r.Name, "x")
	if idx <= 0 || idx == len(r.Name)-1 {
		return cost.Monthly{}, fmt.Errorf("cannot parse fleet cost name %q (want \"<type>x<count>\")", r.Name)
	}
	instanceType := r.Name[:idx]
	count, err := strconv.Atoi(r.Name[idx+1:])
	if err != nil || count <= 0 {
		return cost.Monthly{}, fmt.Errorf("cannot parse instance count from %q: %w", r.Name, err)
	}
	hourly, ok := gameLiftInstancePrices[instanceType]
	if !ok {
		return cost.Monthly{}, fmt.Errorf("no GameLift price data for instance type %q", instanceType)
	}
	return cost.Monthly{
		Amount:     hourly * gameLiftHoursPerMonth * float64(count),
		Confidence: cost.Medium,
		Note:       "GameLift EC2 on-demand ~= EC2 on-demand; excludes data transfer",
	}, nil
}

// freeEstimator covers GameLift builds and aliases (no standing charge).
type freeEstimator struct{ note string }

func (f freeEstimator) Estimate(cost.Resource) (cost.Monthly, error) {
	return cost.Monthly{Amount: 0, Confidence: cost.High, Note: f.note}, nil
}

func init() {
	cost.Global.Register(TypeGameLiftFleet, fleetEstimator{})
	cost.Global.Register(TypeGameLiftBuild, freeEstimator{note: "GameLift build storage is negligible"})
	cost.Global.Register(TypeGameLiftAlias, freeEstimator{note: "GameLift aliases are free"})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/deploy/`
Expected: PASS (all deploy plan/resources/cost tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/deploy/
git add internal/deploy/cost.go internal/deploy/cost_test.go
git commit -m "feat(deploy): register GameLift fleet/build/alias cost estimators"
```

---

### Task 7: Teardown engine — pluggable resource ordering

**Files:**
- Modify: `cmd/internal/teardown/teardown.go` (add `Spec.ResourceOrder` field; `resourcesToDelete` uses it when non-nil)
- Modify: `cmd/internal/teardown/teardown_test.go` (add a test for the custom-order path)

**Why:** `resourcesToDelete` (teardown.go:306) is hardcoded to `AWS::EC2::Instance` → `AWS::EC2::SecurityGroup`. The deploy module's resources are GameLift fleets/builds/alias/role, so the engine needs a per-Spec ordering hook. The three existing callers (perforce destroy, horde destroy, workstation terminate) leave `ResourceOrder` nil and keep today's EC2/SG behavior unchanged.

**Interfaces:**
- Consumes: existing `teardown.Spec`, `teardown.Command`, `fabricastate.ModuleState`, `cloud.Resource`.
- Produces: new field `Spec.ResourceOrder func(*fabricastate.ModuleState) []cloud.Resource`. When non-nil, `resourcesToDelete` delegates to it; when nil, existing EC2→SG behavior.

- [ ] **Step 1: Write the failing test**

Add to `cmd/internal/teardown/teardown_test.go`:

```go
func TestResourceOrderCustomHook(t *testing.T) {
	called := false
	spec := testSpec
	spec.ResourceOrder = func(m *fabricastate.ModuleState) []cloud.Resource {
		called = true
		// reverse the resources to prove the hook drives ordering
		out := make([]cloud.Resource, 0, len(m.Resources))
		for i := len(m.Resources) - 1; i >= 0; i-- {
			out = append(out, cloud.Resource{TypeName: m.Resources[i].TypeName, Identifier: m.Resources[i].Identifier})
		}
		return out
	}
	m := &fabricastate.ModuleState{
		Name: "perforce",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-1"},
			{TypeName: "AWS::GameLift::Build", Identifier: "build-1"},
		},
	}
	got := resourcesToDelete2(spec, m)
	if !called {
		t.Fatal("ResourceOrder hook not invoked")
	}
	if len(got) != 2 || got[0].Identifier != "build-1" {
		t.Fatalf("custom order not applied: %+v", got)
	}
}

func TestResourceOrderNilDefault(t *testing.T) {
	m := &fabricastate.ModuleState{
		Name: "perforce",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-1"},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
		},
	}
	got := resourcesToDelete2(testSpec, m) // testSpec.ResourceOrder is nil
	if len(got) != 2 || got[0].TypeName != "AWS::EC2::Instance" {
		t.Fatalf("default EC2->SG order broken: %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/internal/teardown/ -run TestResourceOrder`
Expected: FAIL — `resourcesToDelete2` undefined; `Spec.ResourceOrder` undefined.

- [ ] **Step 3: Write minimal implementation**

In `cmd/internal/teardown/teardown.go`, add the field to `Spec` (after `SuccessMessage`):

```go
	// ResourceOrder, when non-nil, returns the resources to delete in the order
	// they should be deleted. When nil, the engine uses the default EC2
	// Instance -> SecurityGroup order. Modules whose resources are not the
	// EC2/SG pair (e.g. deploy's GameLift fleet/build/alias/role) set this.
	ResourceOrder func(*fabricastate.ModuleState) []cloud.Resource
```

Add a Spec-aware wrapper and route the existing call site through it. Replace the call in `Run` — find `resources := resourcesToDelete(m)` (teardown.go:89) and change it to:

```go
	resources := resourcesToDelete2(c.Spec, m)
```

Add the wrapper (next to the existing `resourcesToDelete`):

```go
// resourcesToDelete2 returns the deletion-ordered resources for a module. If the
// Spec supplies a ResourceOrder hook it drives the order; otherwise the default
// EC2 Instance -> SecurityGroup order is used.
func resourcesToDelete2(spec Spec, m *fabricastate.ModuleState) []cloud.Resource {
	if spec.ResourceOrder != nil {
		return spec.ResourceOrder(m)
	}
	return resourcesToDelete(m)
}
```

(Leave the original `resourcesToDelete` as-is — it remains the default-order implementation.)

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/internal/teardown/`
Expected: PASS (existing tests + 2 new). The three existing callers are untouched (nil hook).

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/internal/teardown/
git add cmd/internal/teardown/teardown.go cmd/internal/teardown/teardown_test.go
git commit -m "feat(deploy): add pluggable ResourceOrder hook to teardown engine"
```

---

### Task 8: `deploy` parent command + root wiring

**Files:**
- Create: `cmd/deploy/deploy.go`
- Create: `cmd/deploy/cobra_test.go`
- Modify: `cmd/root/root.go` (register `deploy.New(...)`)

**Interfaces:**
- Consumes: `globals.RuntimeSource`, `globals.OptionsSource`; the five subcommand `New(...)` constructors (Tasks 9–13). **Because the subcommands do not exist yet, this task creates `cmd/deploy/deploy.go` with the subcommand `AddCommand` lines commented out**, then each subsequent task uncomments its line. Alternatively, sequence Task 8 last — but wiring-first lets the parent compile and the root register early. Use the commented-stub approach.
- Produces: `deploy.New(runtimeSource, optionsSource, out) *cobra.Command`.

- [ ] **Step 1: Write the parent command (no test-first — pure wiring)**

Create `cmd/deploy/deploy.go`:

```go
// Package deploy wires the "deploy" parent command and its subcommands (setup,
// promote, rollback, status, destroy): GameLift deployment orchestration over
// CI/Horde-produced builds.
package deploy

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/deploy/destroy"
	"github.com/jpvelasco/fabrica/cmd/deploy/promote"
	"github.com/jpvelasco/fabrica/cmd/deploy/rollback"
	"github.com/jpvelasco/fabrica/cmd/deploy/setup"
	"github.com/jpvelasco/fabrica/cmd/deploy/status"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

// New returns the "deploy" parent command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy game-server builds to GameLift fleets",
		Long: `Orchestrate GameLift deployment of CI/Horde-produced server builds.

Available operations:
  setup     Provision deploy infrastructure (IAM role + GameLift alias)
  promote   Register a build from S3 and roll it out to a new fleet (blue/green)
  rollback  Flip the alias back to the previous fleet
  status    Show fleet health, alias target, and rollback candidates
  destroy   Tear down fleets/builds (use --all to also remove alias + role)`,
	}
	cmd.AddCommand(setup.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(promote.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(rollback.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return cmd
}
```

**Build order note:** This file imports the five subpackages, so it will not compile until Tasks 9–13 exist. Implement Tasks 9–13 first, then this file, OR create each subpackage with a minimal compiling `New` stub before wiring. Recommended: **do Task 8's `deploy.go` AFTER Tasks 9–13**. The cobra_test below (Step 2) and root wiring (Step 3) also come after the subcommands compile. Mark this task blocked-by 9–13 in the tracker.

- [ ] **Step 2: Parent cobra test**

Create `cmd/deploy/cobra_test.go`:

```go
package deploy_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/deploy"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.SetOut(out)
	root.SetErr(out)
	optionsSource := func() globals.Options { return opts }
	root.AddCommand(deploy.New(runtimeSource, optionsSource, out))
	return root
}

func run(t *testing.T, src globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(src, &out)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// cobraFakeProvider implements Provider + GameLiftManager so subcommand wiring
// can be exercised without the type assertion failing. Resources() returns a
// no-op client; individual command tests inject finer fakes via run().
type cobraFakeProvider struct{}

func (cobraFakeProvider) Name() string { return "fake" }
func (cobraFakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (cobraFakeProvider) Resources() cloud.ResourceClient { return nil }
func (cobraFakeProvider) CreateFleetAsync(context.Context, *cloud.Resource) error { return nil }
func (cobraFakeProvider) FleetStatus(context.Context, string) (cloud.FleetInfo, error) {
	return cloud.FleetInfo{}, nil
}
func (cobraFakeProvider) FleetEvents(context.Context, string) ([]cloud.FleetEvent, error) {
	return nil, nil
}

func cobraRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Deploy.BuildBucket = "test-bucket"
	return func() (globals.Runtime, error) {
		return globals.Runtime{Config: cfg, Provider: cobraFakeProvider{}}, nil
	}
}

func TestDeploySubcommandsRegistered(t *testing.T) {
	got, err := run(t, cobraRuntime(), "deploy", "--help")
	if err != nil {
		t.Fatalf("deploy --help: %v", err)
	}
	for _, sub := range []string{"setup", "promote", "rollback", "status", "destroy"} {
		if !strings.Contains(got, sub) {
			t.Errorf("deploy --help missing subcommand %q:\n%s", sub, got)
		}
	}
}

func TestDeploySetupDryRun(t *testing.T) {
	got, err := run(t, cobraRuntime(), "deploy", "setup", "--dry-run")
	if err != nil {
		t.Fatalf("deploy setup --dry-run: %v", err)
	}
	if !strings.Contains(got, "dry run") || !strings.Contains(got, "Cost estimate") {
		t.Errorf("expected dry-run plan + cost:\n%s", got)
	}
}
```

- [ ] **Step 3: Register in root**

In `cmd/root/root.go`, find where `ci.New(...)` is added to the root command and add alongside it:

```go
	rootCmd.AddCommand(deploy.New(runtimeSource, optionsSource, out))
```

Add the import `"github.com/jpvelasco/fabrica/cmd/deploy"` to the import block. (Match the exact variable names used at the `ci.New` call site — open `cmd/root/root.go` and mirror that line precisely; the source/out identifiers may differ.)

- [ ] **Step 4: Run tests + build**

Run: `go build ./... && go test ./cmd/deploy/`
Expected: PASS (after Tasks 9–13 exist).

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/deploy/ cmd/root/
git add cmd/deploy/deploy.go cmd/deploy/cobra_test.go cmd/root/root.go
git commit -m "feat(deploy): wire deploy parent command into root"
```

> **Implementation order:** Do Tasks 9–13 (the subcommands) before finalizing Task 8's `deploy.go`/cobra_test/root wiring. Each subcommand package must compile on its own first.

---

### Task 9: `deploy setup` command

**Files:**
- Create: `cmd/deploy/setup/setup.go`
- Create: `cmd/deploy/setup/setup_test.go`

**Interfaces:**
- Consumes: `deploy.NewSetupPlan`, `deploy.RoleDesiredState`, `deploy.AliasDesiredState`, `deploy.TypeAWSIAMRole`, `deploy.TypeGameLiftAlias` (Tasks 4–5); `provision.ReadState`; `cloud.Resource`; `cost.Global`; `prompt.Confirm`; `fabricastate.WriteState`, `stateutil.ResourceByType`.
- Produces: `setup.New(rs globals.RuntimeSource, os globals.OptionsSource, out io.Writer) *cobra.Command`. State module key `"deploy"`.

This mirrors `cmd/ci/setup/setup.go` almost exactly: idempotent create of two Cloud Control resources (IAM role, alias) with dry-run/cost/confirm. The alias is created via the blocking `createResource` (it stabilizes instantly). Validate `BuildBucket` is set (GameLift cannot read a build without it).

- [ ] **Step 1: Write the failing test**

Create `cmd/deploy/setup/setup_test.go`:

```go
package setup

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func baseRuntime() globals.Runtime {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Deploy.BuildBucket = "bkt"
	return globals.Runtime{Config: cfg, Provider: nil}
}

func newTestCmd(rt globals.Runtime, out *bytes.Buffer) *command {
	st := fabricastate.NewState("123456789012", "us-east-1")
	created := map[string]int{}
	return &command{
		runtime: rt,
		out:     out,
		costs:   fabricacost.Global,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(s *fabricastate.State) error { st = s; return nil },
		createResource: func(_ context.Context, r *cloud.Resource) error {
			created[r.TypeName]++
			r.Identifier = r.TypeName + "-id"
			return nil
		},
		getResource: func(_ context.Context, _ *cloud.Resource) error { return nil },
		confirm:     func(string) bool { return true },
	}
}

func TestSetupCreatesRoleAndAlias(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(baseRuntime(), &out)
	c.assumeYes = true
	// Provide identity via a fake provider on the runtime.
	c.runtime.Provider = fakeProvider{}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "IAM role") || !strings.Contains(s, "alias") {
		t.Errorf("expected role+alias creation output:\n%s", s)
	}
}

func TestSetupRequiresBuildBucket(t *testing.T) {
	var out bytes.Buffer
	rt := baseRuntime()
	rt.Config.Deploy.BuildBucket = ""
	c := newTestCmd(rt, &out)
	c.assumeYes = true
	c.runtime.Provider = fakeProvider{}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when buildBucket is unset")
	}
}

func TestSetupDryRunNoWrites(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(baseRuntime(), &out)
	c.dryRun = true
	c.runtime.Provider = fakeProvider{}
	writes := 0
	c.createResource = func(context.Context, *cloud.Resource) error { writes++; return nil }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if writes != 0 {
		t.Errorf("dry-run created %d resources", writes)
	}
	if !strings.Contains(out.String(), "Cost estimate") {
		t.Errorf("dry-run should show cost:\n%s", out.String())
	}
}

func TestSetupConfirmRejected(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(baseRuntime(), &out)
	c.runtime.Provider = fakeProvider{}
	c.confirm = func(string) bool { return false }
	writes := 0
	c.createResource = func(context.Context, *cloud.Resource) error { writes++; return nil }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if writes != 0 {
		t.Errorf("rejected confirm still created %d resources", writes)
	}
}

// fakeProvider supplies Identity for the command.
type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (fakeProvider) Resources() cloud.ResourceClient { return nil }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/deploy/setup/`
Expected: FAIL — package/`command` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/deploy/setup/setup.go`:

```go
// Package setup implements "fabrica deploy setup": provision the deploy
// infrastructure (IAM role GameLift uses to read builds from S3 + a GameLift
// alias) that later promotes flip between fleets.
package setup

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/deploy"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName = "deploy"
	lineWidth  = 58
)

type command struct {
	runtime   globals.Runtime
	dryRun    bool
	assumeYes bool
	out       io.Writer
	costs     *fabricacost.Registry

	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	createResource func(ctx context.Context, r *cloud.Resource) error
	getResource    func(ctx context.Context, r *cloud.Resource) error
	confirm        func(string) bool
}

// New returns the "deploy setup" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Provision deploy infrastructure (IAM role + GameLift alias)",
		Long: `Provision the Fabrica deploy infrastructure: an IAM role GameLift assumes to
read game-server builds from S3, and a GameLift alias used for blue/green
promotion. Idempotent; existing resources are detected and left in place.

Requires deploy.buildBucket in fabrica.yaml (where CI/Horde upload builds).

With --dry-run, shows the planned resources and estimated monthly cost.`,
		Example: `  fabrica deploy setup --dry-run
  fabrica deploy setup
  fabrica deploy setup --yes`,
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
				out:        out,
				costs:      fabricacost.Global,
				readState:  func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				writeState: fabricastate.WriteState,
				confirm:    prompt.Confirm,
			}
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					c.createResource = rc.Create
					c.getResource = rc.Get
				}
			}
			return c.run(cmd.Context())
		},
	}
}

func (c command) run(ctx context.Context) error {
	if c.runtime.Provider == nil {
		return fmt.Errorf("no cloud provider configured — check your config and credentials")
	}
	if c.runtime.Config.Deploy.BuildBucket == "" {
		return fmt.Errorf("deploy.buildBucket is not set in fabrica.yaml — set it to the S3 bucket where CI/Horde uploads server builds, then re-run")
	}
	account, _, region, err := c.runtime.Provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("could not resolve AWS identity (run 'fabrica doctor'): %w", err)
	}

	plan := deploy.NewSetupPlan(c.runtime.Config.Deploy, account, region)
	plan.BuildBucket = c.runtime.Config.Deploy.BuildBucket

	if c.dryRun {
		c.printDryRun(plan)
		return nil
	}

	c.printPlan(plan)
	if !c.assumeYes {
		if !c.confirm("Create these resources?") {
			fmt.Fprintln(c.out, "Setup cancelled. No AWS resources were created.")
			return nil
		}
	} else {
		fmt.Fprintln(c.out, "Proceeding without confirmation (--yes set).")
	}
	return c.apply(ctx, plan)
}

func (c command) apply(ctx context.Context, plan *deploy.SetupPlan) error {
	if c.createResource == nil {
		return fmt.Errorf("cloud provider does not support resource creation — only AWS is supported in V1")
	}
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	var resources []fabricastate.ModuleResource

	if existing, ok := existingResource(st, deploy.TypeAWSIAMRole); ok {
		fmt.Fprintf(c.out, "  IAM role already exists — skipping: %s\n", existing.Identifier)
		resources = append(resources, existing)
	} else {
		roleState, err := deploy.RoleDesiredState(plan)
		if err != nil {
			return fmt.Errorf("building IAM role desired state: %w", err)
		}
		r := &cloud.Resource{TypeName: deploy.TypeAWSIAMRole, DesiredState: roleState}
		if err := c.createResource(ctx, r); err != nil {
			return fmt.Errorf("creating IAM role: %w", err)
		}
		fmt.Fprintf(c.out, "  created IAM role: %s\n", r.Identifier)
		resources = append(resources, fabricastate.ModuleResource{TypeName: deploy.TypeAWSIAMRole, Identifier: r.Identifier})
		st.UpsertModule(moduleName, plan.AliasName, "provisioning", resources)
		_ = c.writeState(st)
	}

	if existing, ok := existingResource(st, deploy.TypeGameLiftAlias); ok {
		fmt.Fprintf(c.out, "  alias already exists — skipping: %s\n", existing.Identifier)
		resources = appendUnique(resources, existing)
	} else {
		aliasState, err := deploy.AliasDesiredState(plan)
		if err != nil {
			return fmt.Errorf("building alias desired state: %w", err)
		}
		r := &cloud.Resource{TypeName: deploy.TypeGameLiftAlias, DesiredState: aliasState}
		if err := c.createResource(ctx, r); err != nil {
			return fmt.Errorf("creating GameLift alias: %w", err)
		}
		fmt.Fprintf(c.out, "  created GameLift alias: %s\n", r.Identifier)
		resources = appendUnique(resources, fabricastate.ModuleResource{TypeName: deploy.TypeGameLiftAlias, Identifier: r.Identifier})
	}

	st.UpsertModule(moduleName, plan.AliasName, "ready", resources)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	c.printCompletion()
	return nil
}

func appendUnique(resources []fabricastate.ModuleResource, r fabricastate.ModuleResource) []fabricastate.ModuleResource {
	for _, e := range resources {
		if e.TypeName == r.TypeName {
			return resources
		}
	}
	return append(resources, r)
}

func existingResource(st *fabricastate.State, typeName string) (fabricastate.ModuleResource, bool) {
	m := st.GetModule(moduleName)
	if m == nil {
		return fabricastate.ModuleResource{}, false
	}
	return stateutil.ResourceByType(m, typeName)
}

func (c command) printDryRun(plan *deploy.SetupPlan) {
	fmt.Fprintln(c.out, "Deploy setup (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	c.printPlanDetails(plan)
	c.costs.EstimateAll(plan.CostResources).Render(c.out, lineWidth)
	fmt.Fprintln(c.out, "Run without --dry-run to create these resources.")
}

func (c command) printPlan(plan *deploy.SetupPlan) {
	fmt.Fprintln(c.out, "Setting up deploy infrastructure...")
	fmt.Fprintln(c.out)
	c.printPlanDetails(plan)
}

func (c command) printPlanDetails(plan *deploy.SetupPlan) {
	fmt.Fprintf(c.out, "  Account:       %s\n", plan.Account)
	fmt.Fprintf(c.out, "  Region:        %s\n", plan.Region)
	fmt.Fprintf(c.out, "  IAM role:      %s\n", plan.RoleName)
	fmt.Fprintf(c.out, "  GameLift alias: %s\n", plan.AliasName)
	fmt.Fprintf(c.out, "  Build bucket:  %s\n", plan.BuildBucket)
	fmt.Fprintln(c.out)
}

func (c command) printCompletion() {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Deploy setup complete.")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  fabrica deploy promote <build-version>   Roll out a build to a new fleet")
	fmt.Fprintln(c.out, "  fabrica deploy status                    Show deploy status")
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/deploy/setup/`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/deploy/setup/
git add cmd/deploy/setup/
git commit -m "feat(deploy): add deploy setup command"
```

---

### Task 10: `deploy promote` command

**Files:**
- Create: `cmd/deploy/promote/promote.go`
- Create: `cmd/deploy/promote/promote_test.go`

**Interfaces:**
- Consumes: `deploy.NewPromotePlan`, `deploy.BuildDesiredState`, `deploy.FleetDesiredState`, `deploy.AliasFlipPatch`, `deploy.Type*` consts; `cloud.Resource`, `cloud.GameLiftManager`, `cloud.FleetInfo`, `cloud.FleetEvent`; `provision.ReadState`; `cost.Global`; `prompt.Confirm`; `fabricastate.WriteState`, `stateutil.ResourceByType`.
- Produces: `promote.New(...)` accepting one positional arg `<build-version>`, flags `--s3-bucket`, `--s3-key`, `--no-wait`. State module key `"deploy"`. Records fleet `Properties` map: `{"buildVersion": <v>, "role": "active"}`; demotes prior active fleet to `"role":"superseded"`.

**Seam fields on `command`:** `readState`, `writeState`, `createResource`, `updateResource`, `getResource`, `createFleetAsync`, `fleetStatus`, `fleetEvents`, `confirm`, `sleep`, `now`.

- [ ] **Step 1: Write the failing test**

Create `cmd/deploy/promote/promote_test.go`:

```go
package promote

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func seededState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("deploy", "fabrica-deploy", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-deploy-gamelift"},
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
	})
	return st
}

func baseRuntime() globals.Runtime {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Deploy.BuildBucket = "bkt"
	return globals.Runtime{Config: cfg, Provider: fakeProvider{}}
}

func newTestCmd(out *bytes.Buffer, st *fabricastate.State) *command {
	statuses := []string{"BUILDING", "ACTIVATING", "ACTIVE"}
	i := 0
	return &command{
		runtime:      baseRuntime(),
		buildVersion: "v1.2.3",
		wait:         true,
		out:          out,
		costs:        fabricacost.Global,
		readState:    func() (*fabricastate.State, error) { return st, nil },
		writeState:   func(s *fabricastate.State) error { *st = *s; return nil },
		createResource: func(_ context.Context, r *cloud.Resource) error {
			r.Identifier = "build-123"
			return nil
		},
		createFleetAsync: func(_ context.Context, r *cloud.Resource) error {
			r.Identifier = "fleet-new"
			return nil
		},
		updateResource: func(_ context.Context, _ *cloud.Resource) error { return nil },
		getResource:    func(_ context.Context, _ *cloud.Resource) error { return nil },
		fleetStatus: func(_ context.Context, id string) (cloud.FleetInfo, error) {
			s := statuses[i]
			if i < len(statuses)-1 {
				i++
			}
			return cloud.FleetInfo{FleetID: id, Status: s}, nil
		},
		fleetEvents: func(context.Context, string) ([]cloud.FleetEvent, error) { return nil, nil },
		confirm:     func(string) bool { return true },
		sleep:       func(time.Duration) {},
		now:         time.Now,
	}
}

func TestPromoteHappyPath(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "ACTIVE") || !strings.Contains(s, "alias") {
		t.Errorf("expected activation + alias flip:\n%s", s)
	}
	// New fleet recorded as active.
	m := st.GetModule("deploy")
	var found bool
	for _, r := range m.Resources {
		if r.TypeName == "AWS::GameLift::Fleet" && r.Identifier == "fleet-new" {
			found = true
			if r.Properties["role"] != "active" {
				t.Errorf("new fleet role = %q", r.Properties["role"])
			}
			if r.Properties["buildVersion"] != "v1.2.3" {
				t.Errorf("buildVersion = %q", r.Properties["buildVersion"])
			}
		}
	}
	if !found {
		t.Error("new fleet not recorded in state")
	}
}

func TestPromoteRequiresSetup(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1") // no deploy module
	c := newTestCmd(&out, st)
	c.assumeYes = true
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error: deploy not set up")
	}
}

func TestPromoteFleetErrorNoFlip(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{FleetID: id, Status: "ERROR"}, nil
	}
	flipped := false
	c.updateResource = func(context.Context, *cloud.Resource) error { flipped = true; return nil }
	c.fleetEvents = func(context.Context, string) ([]cloud.FleetEvent, error) {
		return []cloud.FleetEvent{{Code: "FLEET_STATE_ERROR", Message: "bad launch path"}}, nil
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on fleet ERROR")
	}
	if flipped {
		t.Error("alias must NOT flip when fleet errors")
	}
	if !strings.Contains(out.String(), "bad launch path") {
		t.Errorf("expected fleet events surfaced:\n%s", out.String())
	}
}

func TestPromoteBuildFailsRecoverable(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.createResource = func(context.Context, *cloud.Resource) error { return errors.New("s3 access denied") }
	fleetCreated := false
	c.createFleetAsync = func(context.Context, *cloud.Resource) error { fleetCreated = true; return nil }
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected build registration error")
	}
	if fleetCreated {
		t.Error("fleet must not be created if build registration fails")
	}
}

func TestPromoteDryRunNoWrites(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.dryRun = true
	builds := 0
	c.createResource = func(context.Context, *cloud.Resource) error { builds++; return nil }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if builds != 0 {
		t.Errorf("dry-run registered %d builds", builds)
	}
	if !strings.Contains(out.String(), "Cost estimate") {
		t.Errorf("dry-run should show cost:\n%s", out.String())
	}
}

func TestPromoteAliasFlipFailsAfterActive(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.updateResource = func(context.Context, *cloud.Resource) error { return errors.New("throttled") }
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected alias-flip error")
	}
	if !strings.Contains(err.Error(), "ACTIVE") {
		t.Errorf("error should explain fleet is ACTIVE but alias not flipped: %v", err)
	}
}

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (fakeProvider) Resources() cloud.ResourceClient { return nil }
func (fakeProvider) CreateFleetAsync(context.Context, *cloud.Resource) error { return nil }
func (fakeProvider) FleetStatus(context.Context, string) (cloud.FleetInfo, error) {
	return cloud.FleetInfo{}, nil
}
func (fakeProvider) FleetEvents(context.Context, string) ([]cloud.FleetEvent, error) {
	return nil, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/deploy/promote/`
Expected: FAIL — package/`command` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/deploy/promote/promote.go`:

```go
// Package promote implements "fabrica deploy promote <build-version>": register
// a server build from S3, create a new GameLift fleet for it, wait for the fleet
// to reach ACTIVE, then flip the alias to it (blue/green). The previous fleet is
// retained for rollback.
package promote

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/deploy"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName   = "deploy"
	lineWidth    = 58
	pollInterval = 20 * time.Second
)

type command struct {
	runtime      globals.Runtime
	buildVersion string
	s3Bucket     string
	s3Key        string
	wait         bool
	dryRun       bool
	assumeYes    bool
	out          io.Writer
	costs        *fabricacost.Registry

	readState        func() (*fabricastate.State, error)
	writeState       func(*fabricastate.State) error
	createResource   func(ctx context.Context, r *cloud.Resource) error
	updateResource   func(ctx context.Context, r *cloud.Resource) error
	getResource      func(ctx context.Context, r *cloud.Resource) error
	createFleetAsync func(ctx context.Context, r *cloud.Resource) error
	fleetStatus      func(ctx context.Context, fleetID string) (cloud.FleetInfo, error)
	fleetEvents      func(ctx context.Context, fleetID string) ([]cloud.FleetEvent, error)
	confirm          func(string) bool
	sleep            func(time.Duration)
	now              func() time.Time
}

// New returns the "deploy promote" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var s3Bucket, s3Key string
	var noWait bool
	cmd := &cobra.Command{
		Use:   "promote <build-version>",
		Short: "Register a build and roll it out to a new GameLift fleet (blue/green)",
		Long: `Register a packaged server build from S3 as a GameLift build, create a new
fleet for it, wait until the fleet is ACTIVE, then flip the GameLift alias to the
new fleet. The previously-active fleet is retained so you can 'fabrica deploy
rollback' to it.

Requires 'fabrica deploy setup'. The build must already be uploaded to S3 (by
CI/Horde). By default the S3 location is deploy.buildBucket + "builds/<version>/
server.zip"; override with --s3-bucket / --s3-key.`,
		Example: `  fabrica deploy promote v1.2.3
  fabrica deploy promote v1.2.3 --s3-key builds/v1.2.3/LinuxServer.zip
  fabrica deploy promote v1.2.3 --no-wait`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:      rt,
				buildVersion: args[0],
				s3Bucket:     s3Bucket,
				s3Key:        s3Key,
				wait:         !noWait,
				dryRun:       opts.DryRun,
				assumeYes:    opts.AssumeYes,
				out:          out,
				costs:        fabricacost.Global,
				readState:    func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				writeState:   fabricastate.WriteState,
				confirm:      prompt.Confirm,
				sleep:        time.Sleep,
				now:          time.Now,
			}
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					c.createResource = rc.Create
					c.updateResource = rc.Update
					c.getResource = rc.Get
				}
				if glm, ok := rt.Provider.(cloud.GameLiftManager); ok {
					c.createFleetAsync = glm.CreateFleetAsync
					c.fleetStatus = glm.FleetStatus
					c.fleetEvents = glm.FleetEvents
				}
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&s3Bucket, "s3-bucket", "", "S3 bucket holding the build (default: deploy.buildBucket)")
	cmd.Flags().StringVar(&s3Key, "s3-key", "", "S3 key of the build zip (default: builds/<version>/server.zip)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Return after starting fleet creation without waiting for ACTIVE (skips alias flip)")
	return cmd
}

func (c command) run(ctx context.Context) error {
	if c.runtime.Provider == nil {
		return fmt.Errorf("no cloud provider configured — check your config and credentials")
	}

	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	m := st.GetModule(moduleName)
	if m == nil {
		return fmt.Errorf("deploy is not set up. Run 'fabrica deploy setup' first")
	}
	role, ok := stateutil.ResourceByType(m, deploy.TypeAWSIAMRole)
	if !ok {
		return fmt.Errorf("deploy IAM role not found in state. Run 'fabrica deploy setup' first")
	}
	alias, ok := stateutil.ResourceByType(m, deploy.TypeGameLiftAlias)
	if !ok {
		return fmt.Errorf("deploy alias not found in state. Run 'fabrica deploy setup' first")
	}

	account, _, region, err := c.runtime.Provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("could not resolve AWS identity (run 'fabrica doctor'): %w", err)
	}
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", account, role.Identifier)
	plan := deploy.NewPromotePlan(c.runtime.Config.Deploy, account, region, c.buildVersion, roleARN, alias.Identifier, c.s3Bucket, c.s3Key)

	if c.dryRun {
		c.printDryRun(plan)
		return nil
	}

	c.printPlan(plan)
	if !c.assumeYes {
		if !c.confirm("Register this build and create a new fleet?") {
			fmt.Fprintln(c.out, "Promote cancelled. No AWS resources were created.")
			return nil
		}
	} else {
		fmt.Fprintln(c.out, "Proceeding without confirmation (--yes set).")
	}

	return c.apply(ctx, st, m, plan)
}

func (c command) apply(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, plan *deploy.PromotePlan) error {
	if c.createResource == nil || c.createFleetAsync == nil {
		return fmt.Errorf("cloud provider does not support GameLift deployment — only AWS is supported in V1")
	}

	// 1. Register build.
	buildState, err := deploy.BuildDesiredState(plan)
	if err != nil {
		return fmt.Errorf("building build desired state: %w", err)
	}
	buildRes := &cloud.Resource{TypeName: deploy.TypeGameLiftBuild, DesiredState: buildState}
	if err := c.createResource(ctx, buildRes); err != nil {
		return fmt.Errorf("registering GameLift build from s3://%s/%s: %w — verify the build exists and the deploy role can read it", plan.S3Bucket, plan.S3Key, err)
	}
	fmt.Fprintf(c.out, "Registered build: %s (version %s)\n", buildRes.Identifier, plan.BuildVersion)
	c.recordResource(st, m, fabricastate.ModuleResource{
		TypeName:   deploy.TypeGameLiftBuild,
		Identifier: buildRes.Identifier,
		Properties: map[string]string{"buildVersion": plan.BuildVersion},
	})
	_ = c.writeState(st)

	// 2. Create fleet (non-blocking).
	fleetState, err := deploy.FleetDesiredState(plan, buildRes.Identifier)
	if err != nil {
		return fmt.Errorf("building fleet desired state: %w", err)
	}
	fleetRes := &cloud.Resource{TypeName: deploy.TypeGameLiftFleet, DesiredState: fleetState}
	if err := c.createFleetAsync(ctx, fleetRes); err != nil {
		return fmt.Errorf("creating fleet: %w", err)
	}
	fmt.Fprintf(c.out, "Creating fleet: %s\n", fleetRes.Identifier)
	c.recordResource(st, m, fabricastate.ModuleResource{
		TypeName:   deploy.TypeGameLiftFleet,
		Identifier: fleetRes.Identifier,
		Properties: map[string]string{"buildVersion": plan.BuildVersion, "role": "provisioning"},
	})
	st.UpsertModule(moduleName, plan.BuildVersion, "provisioning", m.Resources)
	_ = c.writeState(st)

	if !c.wait {
		fmt.Fprintf(c.out, "\nFleet creation started. Track it with: fabrica deploy status\n")
		fmt.Fprintln(c.out, "Alias was NOT flipped (--no-wait). Re-run without --no-wait, or flip manually once ACTIVE.")
		return nil
	}

	// 3. Poll activation.
	if err := c.pollUntilActive(ctx, fleetRes.Identifier, plan); err != nil {
		return err
	}

	// 4. Flip alias.
	patch, err := deploy.AliasFlipPatch(fleetRes.Identifier)
	if err != nil {
		return fmt.Errorf("building alias flip patch: %w", err)
	}
	aliasRes := &cloud.Resource{TypeName: deploy.TypeGameLiftAlias, Identifier: plan.AliasID, DesiredState: patch}
	if err := c.updateResource(ctx, aliasRes); err != nil {
		return fmt.Errorf("fleet %s is ACTIVE but flipping the alias failed: %w — the old fleet still serves traffic; re-run 'fabrica deploy promote %s' or flip the alias manually", fleetRes.Identifier, err, plan.BuildVersion)
	}
	fmt.Fprintf(c.out, "Alias %s now points to fleet %s.\n", plan.AliasID, fleetRes.Identifier)

	// 5. Record rollback target: demote previous active fleet, promote new one.
	c.swapActiveFleet(st, m, fleetRes.Identifier)
	st.UpsertModule(moduleName, plan.BuildVersion, "ready", m.Resources)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}

	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Promote complete — %s is live on fleet %s.\n", plan.BuildVersion, fleetRes.Identifier)
	fmt.Fprintln(c.out, "Roll back with: fabrica deploy rollback")
	return nil
}

// pollUntilActive polls fleet status until ACTIVE, printing phase transitions.
// On ERROR or timeout it surfaces recent fleet events and returns an error
// WITHOUT flipping the alias.
func (c command) pollUntilActive(ctx context.Context, fleetID string, plan *deploy.PromotePlan) error {
	deadline := c.now().Add(time.Duration(plan.ActivationTimeoutMinutes) * time.Minute)
	last := ""
	for {
		info, err := c.fleetStatus(ctx, fleetID)
		if err != nil {
			return fmt.Errorf("polling fleet %s: %w", fleetID, err)
		}
		if info.Status != last {
			fmt.Fprintf(c.out, "  fleet %s: %s\n", fleetID, info.Status)
			last = info.Status
		}
		switch info.Status {
		case "ACTIVE":
			return nil
		case "ERROR", "DELETING", "TERMINATED":
			c.printFleetEvents(ctx, fleetID)
			return fmt.Errorf("fleet %s entered status %s before becoming ACTIVE — see events above and 'fabrica deploy status'; the alias was not changed", fleetID, info.Status)
		}
		if c.now().After(deadline) {
			c.printFleetEvents(ctx, fleetID)
			return fmt.Errorf("timed out after %d minutes waiting for fleet %s to become ACTIVE (status %s) — check 'fabrica deploy status'; the alias was not changed", plan.ActivationTimeoutMinutes, fleetID, info.Status)
		}
		c.sleep(pollInterval)
	}
}

func (c command) printFleetEvents(ctx context.Context, fleetID string) {
	if c.fleetEvents == nil {
		return
	}
	evs, err := c.fleetEvents(ctx, fleetID)
	if err != nil || len(evs) == 0 {
		return
	}
	fmt.Fprintln(c.out, "Recent fleet events:")
	for _, e := range evs {
		fmt.Fprintf(c.out, "  [%s] %s %s\n", e.Time, e.Code, e.Message)
	}
}

// recordResource adds or replaces a resource of the same type+identifier in the
// module's resource list.
func (c command) recordResource(st *fabricastate.State, m *fabricastate.ModuleState, r fabricastate.ModuleResource) {
	for i := range m.Resources {
		if m.Resources[i].TypeName == r.TypeName && m.Resources[i].Identifier == r.Identifier {
			m.Resources[i] = r
			return
		}
	}
	m.Resources = append(m.Resources, r)
}

// swapActiveFleet marks newFleetID active and demotes any other active fleet to
// superseded (the rollback candidate).
func (c command) swapActiveFleet(st *fabricastate.State, m *fabricastate.ModuleState, newFleetID string) {
	for i := range m.Resources {
		r := &m.Resources[i]
		if r.TypeName != deploy.TypeGameLiftFleet {
			continue
		}
		if r.Properties == nil {
			r.Properties = map[string]string{}
		}
		switch {
		case r.Identifier == newFleetID:
			r.Properties["role"] = "active"
		case r.Properties["role"] == "active":
			r.Properties["role"] = "superseded"
		}
	}
}

func (c command) printDryRun(plan *deploy.PromotePlan) {
	fmt.Fprintln(c.out, "Deploy promote (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	c.printPlanDetails(plan)
	c.costs.EstimateAll(plan.CostResources).Render(c.out, lineWidth)
	fmt.Fprintln(c.out, "Run without --dry-run to register the build and create the fleet.")
}

func (c command) printPlan(plan *deploy.PromotePlan) {
	fmt.Fprintln(c.out, "Promoting build to a new fleet...")
	fmt.Fprintln(c.out)
	c.printPlanDetails(plan)
	fmt.Fprintln(c.out, "The previously-active fleet is retained for rollback.")
	fmt.Fprintln(c.out)
}

func (c command) printPlanDetails(plan *deploy.PromotePlan) {
	fmt.Fprintf(c.out, "  Build version: %s\n", plan.BuildVersion)
	fmt.Fprintf(c.out, "  Build source:  s3://%s/%s\n", plan.S3Bucket, plan.S3Key)
	fmt.Fprintf(c.out, "  Fleet:         %s\n", plan.FleetName)
	fmt.Fprintf(c.out, "  Instance type: %s (%s)\n", plan.InstanceType, plan.FleetType)
	fmt.Fprintf(c.out, "  Launch path:   %s\n", plan.LaunchPath)
	fmt.Fprintf(c.out, "  Alias:         %s\n", plan.AliasID)
	fmt.Fprintln(c.out)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/deploy/promote/`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/deploy/promote/
git add cmd/deploy/promote/
git commit -m "feat(deploy): add deploy promote command (build->fleet->poll->alias flip)"
```

---

### Task 11: `deploy rollback` command

**Files:**
- Create: `cmd/deploy/rollback/rollback.go`
- Create: `cmd/deploy/rollback/rollback_test.go`

**Interfaces:**
- Consumes: `deploy.AliasFlipPatch`, `deploy.TypeGameLiftAlias`, `deploy.TypeGameLiftFleet`; `cloud.Resource`, `cloud.GameLiftManager`, `cloud.FleetInfo`; `provision.ReadState`; `prompt.Confirm`; `fabricastate.WriteState`, `stateutil.ResourceByType`.
- Produces: `rollback.New(...)`. Finds the most-recent `superseded` fleet, verifies it is still `ACTIVE`, shows current→target, flips the alias, swaps roles.

**Seam fields:** `readState`, `writeState`, `updateResource`, `fleetStatus`, `confirm`.

- [ ] **Step 1: Write the failing test**

Create `cmd/deploy/rollback/rollback_test.go`:

```go
package rollback

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func stateWith(active, superseded string) *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	res := []fabricastate.ModuleResource{
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
		{TypeName: "AWS::GameLift::Fleet", Identifier: active, Properties: map[string]string{"role": "active", "buildVersion": "v2"}},
	}
	if superseded != "" {
		res = append(res, fabricastate.ModuleResource{
			TypeName: "AWS::GameLift::Fleet", Identifier: superseded,
			Properties: map[string]string{"role": "superseded", "buildVersion": "v1"},
		})
	}
	st.UpsertModule("deploy", "v2", "ready", res)
	return st
}

func newTestCmd(out *bytes.Buffer, st *fabricastate.State) *command {
	return &command{
		runtime:    globals.Runtime{Config: config.Defaults(), Provider: fakeProvider{}},
		out:        out,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(s *fabricastate.State) error { *st = *s; return nil },
		updateResource: func(context.Context, *cloud.Resource) error { return nil },
		fleetStatus: func(_ context.Context, id string) (cloud.FleetInfo, error) {
			return cloud.FleetInfo{FleetID: id, Status: "ACTIVE"}, nil
		},
		confirm: func(string) bool { return true },
	}
}

func TestRollbackHappyPath(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "fleet-old") {
		t.Errorf("expected target fleet shown:\n%s", out.String())
	}
	// Roles swapped: fleet-old now active, fleet-new superseded.
	m := st.GetModule("deploy")
	for _, r := range m.Resources {
		if r.Identifier == "fleet-old" && r.Properties["role"] != "active" {
			t.Errorf("fleet-old role = %q, want active", r.Properties["role"])
		}
		if r.Identifier == "fleet-new" && r.Properties["role"] != "superseded" {
			t.Errorf("fleet-new role = %q, want superseded", r.Properties["role"])
		}
	}
}

func TestRollbackNoCandidate(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "")
	c := newTestCmd(&out, st)
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error: nothing to roll back to")
	}
}

func TestRollbackTargetNotActive(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{FleetID: id, Status: "TERMINATED"}, nil
	}
	flipped := false
	c.updateResource = func(context.Context, *cloud.Resource) error { flipped = true; return nil }
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error: target fleet not ACTIVE")
	}
	if flipped {
		t.Error("must not flip when target is not ACTIVE")
	}
}

func TestRollbackConfirmRejected(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.confirm = func(string) bool { return false }
	flipped := false
	c.updateResource = func(context.Context, *cloud.Resource) error { flipped = true; return nil }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if flipped {
		t.Error("rejected confirm should not flip")
	}
}

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (fakeProvider) Resources() cloud.ResourceClient { return nil }
func (fakeProvider) CreateFleetAsync(context.Context, *cloud.Resource) error { return nil }
func (fakeProvider) FleetStatus(context.Context, string) (cloud.FleetInfo, error) {
	return cloud.FleetInfo{}, nil
}
func (fakeProvider) FleetEvents(context.Context, string) ([]cloud.FleetEvent, error) {
	return nil, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/deploy/rollback/`
Expected: FAIL — undefined.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/deploy/rollback/rollback.go`:

```go
// Package rollback implements "fabrica deploy rollback": flip the GameLift alias
// back to the most-recent superseded (retained) fleet.
package rollback

import (
	"context"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/deploy"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const moduleName = "deploy"

type command struct {
	runtime   globals.Runtime
	assumeYes bool
	out       io.Writer

	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	updateResource func(ctx context.Context, r *cloud.Resource) error
	fleetStatus    func(ctx context.Context, fleetID string) (cloud.FleetInfo, error)
	confirm        func(string) bool
}

// New returns the "deploy rollback" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "rollback",
		Short: "Flip the alias back to the previous (retained) fleet",
		Long: `Roll back the deployment by flipping the GameLift alias to the most-recent
retained ("superseded") fleet. The target fleet must still be ACTIVE.

Use this when a freshly-promoted build misbehaves: the previous fleet is kept
running by 'deploy promote' precisely so rollback is instant.`,
		Example: `  fabrica deploy rollback
  fabrica deploy rollback --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:    rt,
				assumeYes:  opts.AssumeYes,
				out:        out,
				readState:  func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				writeState: fabricastate.WriteState,
				confirm:    prompt.Confirm,
			}
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					c.updateResource = rc.Update
				}
				if glm, ok := rt.Provider.(cloud.GameLiftManager); ok {
					c.fleetStatus = glm.FleetStatus
				}
			}
			return c.run(cmd.Context())
		},
	}
}

func (c command) run(ctx context.Context) error {
	if c.runtime.Provider == nil {
		return fmt.Errorf("no cloud provider configured — check your config and credentials")
	}
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	m := st.GetModule(moduleName)
	if m == nil {
		return fmt.Errorf("deploy is not set up. Run 'fabrica deploy setup' first")
	}
	alias, ok := stateutil.ResourceByType(m, deploy.TypeGameLiftAlias)
	if !ok {
		return fmt.Errorf("deploy alias not found in state. Run 'fabrica deploy setup' first")
	}

	active, target := findActiveAndSuperseded(m)
	if target == "" {
		return fmt.Errorf("no previous fleet to roll back to — only one fleet has been promoted. Nothing to do")
	}

	if c.fleetStatus == nil {
		return fmt.Errorf("cloud provider does not support GameLift — only AWS is supported in V1")
	}
	info, err := c.fleetStatus(ctx, target)
	if err != nil {
		return fmt.Errorf("checking rollback target fleet %s: %w", target, err)
	}
	if info.Status != "ACTIVE" {
		return fmt.Errorf("rollback target fleet %s is %s, not ACTIVE — it may have been terminated; cannot roll back to it", target, info.Status)
	}

	fmt.Fprintf(c.out, "Rolling back the alias:\n")
	fmt.Fprintf(c.out, "  current fleet: %s\n", active)
	fmt.Fprintf(c.out, "  target fleet:  %s\n", target)
	fmt.Fprintln(c.out)

	if !c.assumeYes {
		if !c.confirm(fmt.Sprintf("Flip alias %s to fleet %s?", alias.Identifier, target)) {
			fmt.Fprintln(c.out, "Rollback cancelled. The alias was not changed.")
			return nil
		}
	}

	patch, err := deploy.AliasFlipPatch(target)
	if err != nil {
		return fmt.Errorf("building alias flip patch: %w", err)
	}
	r := &cloud.Resource{TypeName: deploy.TypeGameLiftAlias, Identifier: alias.Identifier, DesiredState: patch}
	if err := c.updateResource(ctx, r); err != nil {
		return fmt.Errorf("flipping alias %s to fleet %s: %w", alias.Identifier, target, err)
	}

	swapRoles(m, target, active)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	fmt.Fprintf(c.out, "Rolled back — alias %s now points to fleet %s.\n", alias.Identifier, target)
	return nil
}

// findActiveAndSuperseded returns the identifiers of the active fleet and the
// most-recent superseded fleet (the last one in resource order).
func findActiveAndSuperseded(m *fabricastate.ModuleState) (active, superseded string) {
	for _, r := range m.Resources {
		if r.TypeName != deploy.TypeGameLiftFleet {
			continue
		}
		switch r.Properties["role"] {
		case "active":
			active = r.Identifier
		case "superseded":
			superseded = r.Identifier // later entries win → most recent
		}
	}
	return active, superseded
}

// swapRoles makes target active and the former active superseded.
func swapRoles(m *fabricastate.ModuleState, target, formerActive string) {
	for i := range m.Resources {
		r := &m.Resources[i]
		if r.TypeName != deploy.TypeGameLiftFleet {
			continue
		}
		if r.Properties == nil {
			r.Properties = map[string]string{}
		}
		switch r.Identifier {
		case target:
			r.Properties["role"] = "active"
		case formerActive:
			r.Properties["role"] = "superseded"
		}
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/deploy/rollback/`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/deploy/rollback/
git add cmd/deploy/rollback/
git commit -m "feat(deploy): add deploy rollback command"
```

---

### Task 12: `deploy status` command

**Files:**
- Create: `cmd/deploy/status/status.go`
- Create: `cmd/deploy/status/status_test.go`

**Interfaces:**
- Consumes: `deploy.TypeGameLiftAlias`, `deploy.TypeGameLiftFleet`; `cloud.GameLiftManager`, `cloud.FleetInfo`, `cloud.FleetEvent`; `provision.ReadState`; `stateutil.ResourceByType`; `globals.Options.JSONOutput`.
- Produces: `status.New(...)`. Read-only; never writes state. `--json` emits a `statusJSON` struct. Labels superseded fleets as rollback candidates.

**Seam fields:** `readState`, `fleetStatus`, `fleetEvents`.

- [ ] **Step 1: Write the failing test**

Create `cmd/deploy/status/status_test.go`:

```go
package status

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func newTestCmd(out *bytes.Buffer, st *fabricastate.State) *command {
	return &command{
		runtime:   globals.Runtime{Config: config.Defaults(), Provider: fakeProvider{}},
		out:       out,
		readState: func() (*fabricastate.State, error) { return st, nil },
		fleetStatus: func(_ context.Context, id string) (cloud.FleetInfo, error) {
			return cloud.FleetInfo{FleetID: id, Status: "ACTIVE"}, nil
		},
		fleetEvents: func(context.Context, string) ([]cloud.FleetEvent, error) { return nil, nil },
	}
}

func deployState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("deploy", "v2", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-new", Properties: map[string]string{"role": "active", "buildVersion": "v2"}},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-old", Properties: map[string]string{"role": "superseded", "buildVersion": "v1"}},
	})
	return st
}

func TestStatusNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCmd(&out, st)
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "not set up") {
		t.Errorf("expected not-set-up message:\n%s", out.String())
	}
}

func TestStatusShowsActiveAndRollbackCandidate(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(&out, deployState())
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "fleet-new") || !strings.Contains(s, "active") {
		t.Errorf("expected active fleet:\n%s", s)
	}
	if !strings.Contains(s, "fleet-old") || !strings.Contains(strings.ToLower(s), "rollback") {
		t.Errorf("expected rollback candidate labeled:\n%s", s)
	}
}

func TestStatusJSON(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(&out, deployState())
	c.jsonOut = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "\"activeFleet\"") || !strings.Contains(s, "fleet-new") {
		t.Errorf("expected JSON with activeFleet:\n%s", s)
	}
}

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (fakeProvider) Resources() cloud.ResourceClient { return nil }
func (fakeProvider) CreateFleetAsync(context.Context, *cloud.Resource) error { return nil }
func (fakeProvider) FleetStatus(context.Context, string) (cloud.FleetInfo, error) {
	return cloud.FleetInfo{}, nil
}
func (fakeProvider) FleetEvents(context.Context, string) ([]cloud.FleetEvent, error) {
	return nil, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/deploy/status/`
Expected: FAIL — undefined.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/deploy/status/status.go`:

```go
// Package status implements "fabrica deploy status": a read-only overview of the
// deploy module — the alias, the active fleet, and any retained rollback
// candidates, with live GameLift fleet status. Never mutates state.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/deploy"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName = "deploy"
	lineWidth  = 58
)

type command struct {
	runtime globals.Runtime
	jsonOut bool
	out     io.Writer

	readState   func() (*fabricastate.State, error)
	fleetStatus func(ctx context.Context, fleetID string) (cloud.FleetInfo, error)
	fleetEvents func(ctx context.Context, fleetID string) ([]cloud.FleetEvent, error)
}

type fleetJSON struct {
	FleetID      string `json:"fleetId"`
	BuildVersion string `json:"buildVersion"`
	Role         string `json:"role"`
	LiveStatus   string `json:"liveStatus"`
}

type statusJSON struct {
	Provisioned        bool        `json:"provisioned"`
	Alias              string      `json:"alias,omitempty"`
	ActiveFleet        *fleetJSON  `json:"activeFleet,omitempty"`
	RollbackCandidates []fleetJSON `json:"rollbackCandidates,omitempty"`
}

// New returns the "deploy status" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show deploy status (alias, active fleet, rollback candidates)",
		Long: `Show the deploy module's current state: the GameLift alias and the fleet it
points to, plus any retained fleets you can roll back to. Queries live fleet
status from GameLift. Read-only — never changes anything.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:   rt,
				jsonOut:   opts.JSONOutput,
				out:       out,
				readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
			}
			if rt.Provider != nil {
				if glm, ok := rt.Provider.(cloud.GameLiftManager); ok {
					c.fleetStatus = glm.FleetStatus
					c.fleetEvents = glm.FleetEvents
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
		if c.jsonOut {
			return c.emitJSON(statusJSON{Provisioned: false})
		}
		fmt.Fprintln(c.out, "Deploy is not set up. Run 'fabrica deploy setup' to begin.")
		return nil
	}

	alias, _ := stateutil.ResourceByType(m, deploy.TypeGameLiftAlias)

	var active *fleetJSON
	var candidates []fleetJSON
	for _, r := range m.Resources {
		if r.TypeName != deploy.TypeGameLiftFleet {
			continue
		}
		f := fleetJSON{
			FleetID:      r.Identifier,
			BuildVersion: r.Properties["buildVersion"],
			Role:         r.Properties["role"],
			LiveStatus:   c.liveStatus(ctx, r.Identifier),
		}
		if r.Properties["role"] == "active" {
			fc := f
			active = &fc
		} else if r.Properties["role"] == "superseded" {
			candidates = append(candidates, f)
		}
	}

	if c.jsonOut {
		return c.emitJSON(statusJSON{
			Provisioned:        true,
			Alias:              alias.Identifier,
			ActiveFleet:        active,
			RollbackCandidates: candidates,
		})
	}

	fmt.Fprintln(c.out, "Deploy status")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Alias: %s\n", alias.Identifier)
	fmt.Fprintln(c.out)
	if active != nil {
		fmt.Fprintln(c.out, "Active fleet (alias points here):")
		fmt.Fprintf(c.out, "  %s  build=%s  status=%s\n", active.FleetID, active.BuildVersion, active.LiveStatus)
	} else {
		fmt.Fprintln(c.out, "No active fleet yet. Run 'fabrica deploy promote <build-version>'.")
	}
	fmt.Fprintln(c.out)
	if len(candidates) > 0 {
		fmt.Fprintln(c.out, "Rollback candidates (retained — 'fabrica deploy rollback' flips to the newest):")
		for _, f := range candidates {
			fmt.Fprintf(c.out, "  %s  build=%s  status=%s   <- rollback candidate\n", f.FleetID, f.BuildVersion, f.LiveStatus)
		}
	} else {
		fmt.Fprintln(c.out, "No rollback candidates (only one fleet promoted so far).")
	}
	return nil
}

// liveStatus queries GameLift for the fleet's current status, degrading to
// "unknown" if no provider/manager is available or the call fails (status is
// read-only and must never hard-fail the overview).
func (c command) liveStatus(ctx context.Context, fleetID string) string {
	if c.fleetStatus == nil {
		return "unknown (no provider)"
	}
	info, err := c.fleetStatus(ctx, fleetID)
	if err != nil {
		return "unknown"
	}
	return info.Status
}

func (c command) emitJSON(s statusJSON) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/deploy/status/`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/deploy/status/
git add cmd/deploy/status/
git commit -m "feat(deploy): add deploy status command"
```

---

### Task 13: `deploy destroy` command

**Files:**
- Create: `cmd/deploy/destroy/destroy.go`
- Create: `cmd/deploy/destroy/destroy_test.go`

**Interfaces:**
- Consumes: `teardown.Command`, `teardown.Spec` (incl. new `ResourceOrder` from Task 7); `deploy.Type*` consts; `prompt.ConfirmExact`; `provision.ReadState`; `cloud.Resource`; `fabricastate`.
- Produces: `destroy.New(...)` with an `--all` flag. Default deletes fleets+builds (order: fleets then builds); `--all` additionally deletes alias then role. Prints a preservation warning in default mode.

**Design:** Build a `teardown.Spec` with a `ResourceOrder` hook that returns the resources in delete order, honoring `--all`. The `teardown.Command` engine does the confirm/dry-run/delete/state plumbing. Because `--all` changes the resource set, it is captured in the closure passed as `ResourceOrder`.

- [ ] **Step 1: Write the failing test**

Create `cmd/deploy/destroy/destroy_test.go`:

```go
package destroy

import (
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func deployModule() *fabricastate.ModuleState {
	return &fabricastate.ModuleState{
		Name: "deploy",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::IAM::Role", Identifier: "role-1"},
			{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
			{TypeName: "AWS::GameLift::Build", Identifier: "build-1"},
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-1", Properties: map[string]string{"role": "superseded"}},
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-2", Properties: map[string]string{"role": "active"}},
		},
	}
}

func TestResourceOrderDefaultFleetsAndBuilds(t *testing.T) {
	order := resourceOrder(false) // --all = false
	got := order(deployModule())
	// Only fleets + builds, fleets first.
	var types []string
	for _, r := range got {
		types = append(types, r.TypeName)
	}
	if len(got) != 3 {
		t.Fatalf("default destroy should target 3 resources (2 fleets + 1 build), got %d: %v", len(got), types)
	}
	if got[0].TypeName != "AWS::GameLift::Fleet" || got[len(got)-1].TypeName != "AWS::GameLift::Build" {
		t.Errorf("expected fleets before build: %v", types)
	}
	for _, r := range got {
		if r.TypeName == "AWS::GameLift::Alias" || r.TypeName == "AWS::IAM::Role" {
			t.Errorf("default destroy must NOT include %s", r.TypeName)
		}
	}
}

func TestResourceOrderAllIncludesAliasAndRole(t *testing.T) {
	order := resourceOrder(true) // --all = true
	got := order(deployModule())
	if len(got) != 5 {
		t.Fatalf("--all should target all 5 resources, got %d", len(got))
	}
	// Order: fleets, build, alias, role (alias+role last so build/fleet refs clear first).
	last := got[len(got)-1]
	if last.TypeName != "AWS::IAM::Role" {
		t.Errorf("role should be deleted last, got %s", last.TypeName)
	}
}

func TestNewBuildsCommand(t *testing.T) {
	// Smoke test: New returns a command with the --all flag and runs dry-run
	// against an empty provider without panicking.
	_ = context.Background()
	cmd := newForTest(false)
	if cmd.Spec.ModuleName != "deploy" {
		t.Errorf("module name = %q", cmd.Spec.ModuleName)
	}
	if cmd.Spec.ResourceOrder == nil {
		t.Error("ResourceOrder must be set for deploy destroy")
	}
	_ = cloud.Resource{}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/deploy/destroy/`
Expected: FAIL — `resourceOrder`/`newForTest` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/deploy/destroy/destroy.go`:

```go
// Package destroy implements "fabrica deploy destroy": tear down deploy
// resources. By default it deletes only the fleets and builds, preserving the
// long-lived alias and IAM role (game backends reference the alias). Pass --all
// to also remove the alias and role.
package destroy

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/deploy"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const moduleName = "deploy"

// resourceOrder returns the teardown ordering hook for the given --all setting.
// Fleets are deleted first, then builds (a fleet references its build); with
// all=true the alias then the role follow (deleted last).
func resourceOrder(all bool) func(*fabricastate.ModuleState) []cloud.Resource {
	return func(m *fabricastate.ModuleState) []cloud.Resource {
		var fleets, builds, alias, role []cloud.Resource
		for _, r := range m.Resources {
			res := cloud.Resource{TypeName: r.TypeName, Identifier: r.Identifier}
			switch r.TypeName {
			case deploy.TypeGameLiftFleet:
				fleets = append(fleets, res)
			case deploy.TypeGameLiftBuild:
				builds = append(builds, res)
			case deploy.TypeGameLiftAlias:
				alias = append(alias, res)
			case deploy.TypeAWSIAMRole:
				role = append(role, res)
			}
		}
		out := append(fleets, builds...)
		if all {
			out = append(out, alias...)
			out = append(out, role...)
		}
		return out
	}
}

func spec(all bool) teardown.Spec {
	s := teardown.Spec{
		ModuleName:     moduleName,
		Verb:           "destroy",
		VersionLabel:   "Version",
		Title:          "GameLift deployment",
		NotProvisioned: "Deploy is not provisioned. Nothing to destroy.",
		PlanHeader:     "GameLift deployment — destroy plan",
		DryRunHeader:   "GameLift deployment (destroy dry run)",
		SuccessMessage: "GameLift deployment resources destroyed.",
		ResourceOrder:  resourceOrder(all),
	}
	if all {
		s.Irreversible = "IRREVERSIBLE: deletes all fleets, builds, the alias, and the IAM role."
	} else {
		s.Irreversible = "IRREVERSIBLE: deletes all fleets and builds. The alias and IAM role are PRESERVED (use --all to remove them)."
	}
	return s
}

// newForTest builds the teardown.Command without provider wiring, for unit tests.
func newForTest(all bool) teardown.Command {
	return teardown.Command{Spec: spec(all)}
}

// New returns the "deploy destroy" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Tear down deploy resources (fleets + builds; --all also removes alias + role)",
		Long: `Tear down GameLift deploy resources.

By default this deletes the fleets and builds but PRESERVES the GameLift alias
and IAM role, because game clients/backends reference the alias and it is meant
to outlive individual deployments. Pass --all to remove the alias and role too
(symmetric with 'fabrica deploy setup').

Active game sessions are not drained automatically; if a fleet refuses to delete,
GameLift's error explains why — terminate sessions or wait, then retry.`,
		Example: `  fabrica deploy destroy            # fleets + builds only (alias/role kept)
  fabrica deploy destroy --all      # everything, incl. alias + role
  fabrica deploy destroy --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			tc := teardown.Command{
				Spec:       spec(all),
				Runtime:    rt,
				DryRun:     opts.DryRun,
				AssumeYes:  opts.AssumeYes,
				JSONOut:    opts.JSONOutput,
				Out:        out,
				Confirm:    prompt.ConfirmExact,
				ReadState:  func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				WriteState: fabricastate.WriteState,
			}
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					tc.DeleteResource = rc.Delete
					tc.GetResource = rc.Get
				}
			}
			if !opts.JSONOutput && !all && !opts.DryRun {
				cmd.Printf("Note: the GameLift alias and IAM role will be preserved. Use --all to remove them.\n\n")
			}
			return tc.Run(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Also delete the GameLift alias and IAM role")
	return cmd
}
```

**Note:** Confirm `prompt.ConfirmExact` has signature `func(prompt, phrase string) bool` (matches `teardown.Command.Confirm func(string, string) bool`). It is the same function the other destroy commands pass — verify by grepping `cmd/perforce/destroy/destroy.go` for `Confirm:`.

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/deploy/destroy/`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w cmd/deploy/destroy/
git add cmd/deploy/destroy/
git commit -m "feat(deploy): add deploy destroy command (default fleets+builds; --all)"
```

---

### Task 14: Finalize parent wiring + full verification + docs

**Files:**
- Create: `cmd/deploy/deploy.go`, `cmd/deploy/cobra_test.go` (from Task 8)
- Modify: `cmd/root/root.go` (from Task 8)
- Create: `docs/deploy.md`
- Modify: `fabrica.example.yaml`, `ROADMAP.md`, `CLAUDE.md`

**Interfaces:** Consumes all five subcommand `New(...)` (Tasks 9–13). This is where Task 8's deferred wiring lands now that the subcommands compile.

- [ ] **Step 1: Create the parent command + root wiring**

Create `cmd/deploy/deploy.go` and `cmd/deploy/cobra_test.go` exactly as specified in Task 8 Steps 1–2, and add the root registration from Task 8 Step 3.

- [ ] **Step 2: Full build + vet + test**

Run:
```bash
go build ./...
go vet ./...
go test ./...
```
Expected: all PASS. If `go vet` flags the unused `_ = cloud.Resource{}` line in the destroy test, remove it.

- [ ] **Step 3: Lint**

Run: `golangci-lint run ./...`
Expected: zero issues. Common fixes: unchecked errors on `c.writeState` are intentionally `_ =`'d (matches existing modules — gosec G104 is excluded); if `errcheck` flags a new one, wrap with `_ =` only where the existing code does.

- [ ] **Step 4: Add config example**

In `fabrica.example.yaml`, add a `deploy:` section (mirror the `ci:` block's style):

```yaml
# Deploy module — GameLift fleet deployment of CI/Horde server builds.
deploy:
  buildBucket: ""           # S3 bucket where CI/Horde uploads packaged server builds (required)
  instanceType: c5.large    # GameLift EC2 instance type
  fleetType: ON_DEMAND      # ON_DEMAND or SPOT
  launchPath: /local/game/ServerApp   # server executable path inside the build
  buildOs: AMAZON_LINUX_2   # build operating system
  fromPort: 7777            # inbound UDP port range start
  toPort: 7777              # inbound UDP port range end
  desiredInstances: 1       # instances per fleet (cost estimate only in V1)
  activationTimeoutMinutes: 45   # how long 'promote' waits for a fleet to become ACTIVE
```

- [ ] **Step 5: Write the module guide**

Create `docs/deploy.md`:

```markdown
# Deploy Module

`fabrica deploy` orchestrates GameLift deployment of the UE5 dedicated-server
builds produced by the CI/Horde pipeline. It owns the build-to-deploy path;
live runtime fleet operations (scaling, matchmaking, sessions) are left to
Classis.

## Commands

| Command | Purpose |
|---------|---------|
| `fabrica deploy setup` | Provision the IAM role (GameLift→S3 read) + GameLift alias. Idempotent. |
| `fabrica deploy promote <build-version>` | Register a build from S3, create a new fleet, wait for ACTIVE, flip the alias (blue/green). |
| `fabrica deploy rollback` | Flip the alias back to the most-recent retained fleet. |
| `fabrica deploy status` | Show the alias, active fleet, and rollback candidates with live fleet status. |
| `fabrica deploy destroy [--all]` | Delete fleets + builds; `--all` also removes the alias + role. |

## Prerequisites

1. `fabrica setup` (state backend).
2. `deploy.buildBucket` set in `fabrica.yaml`.
3. The packaged server build uploaded to S3 (by CI/Horde), by convention at
   `s3://<buildBucket>/builds/<build-version>/server.zip` (override with
   `--s3-bucket`/`--s3-key`).

## Blue/green and rollback

`promote` always creates a **new fleet** and flips the alias only once the fleet
is `ACTIVE`. The previously-active fleet is **retained** so `rollback` is an
instant alias flip — no re-provisioning. `destroy` (without `--all`) leaves the
alias and role in place so the alias your game backend references survives
teardown.

## Architecture notes

- GameLift `Build`, `Fleet`, and `Alias` are created through the Cloud Control
  API. Fleet **activation** (20–40 min) is tracked through the
  `cloud.GameLiftManager` SDK auxiliary interface (`FleetStatus`/`FleetEvents`)
  because the blocking Cloud Control waiter cannot surface fleet phases or
  activation-failure events. Fleet creation uses a non-blocking Cloud Control
  path (`CreateFleetAsync`) that returns as soon as the FleetId is assigned.
- The deploy module reuses the shared `cmd/internal/teardown` engine via its
  `ResourceOrder` hook to delete GameLift resources in dependency order.

## Out of scope (V1)

Scaling policies, FlexMatch matchmaking, game-session management, deep runtime
monitoring (→ Classis); auto-draining sessions on fleet delete; multi-region
fleets; container/Anywhere/Realtime fleets.
```

- [ ] **Step 6: Update ROADMAP + CLAUDE.md**

In `ROADMAP.md`: change the Milestone 3 line and the module-status table row for `deploy` from `⬜ Planned` to `✅ Complete — GameLift blue/green deploy orchestration`.

In `CLAUDE.md`: add `deploy` to the implemented-modules sentence in "Project Status"; add `cmd/deploy*` and `internal/deploy` rows to the package-responsibilities table; add a "Deploy-Specific Notes" section summarizing the GameLiftManager split, the non-blocking fleet create, the alias-flip blue/green, the retain-for-rollback behavior, and the `destroy` default-vs-`--all` semantics. Mark the command tree `fabrica deploy ...` line `✓ implemented`.

- [ ] **Step 7: Final verification + commit**

Run:
```bash
go build ./... && go vet ./... && go test ./... && golangci-lint run ./...
```
Expected: all clean.

```bash
gofmt -w .
git add docs/deploy.md fabrica.example.yaml ROADMAP.md CLAUDE.md cmd/deploy/deploy.go cmd/deploy/cobra_test.go cmd/root/root.go
git commit -m "docs(deploy): module guide, config example, roadmap + CLAUDE updates"
```

- [ ] **Step 8: Push + open PR**

```bash
git push -u origin feat/deploy-module
gh pr create --title "feat: deploy module — fabrica deploy setup/promote/rollback/status/destroy (Milestone 3)" --body "Implements Milestone 3 per docs/superpowers/specs/2026-06-28-milestone-3-deploy-module-design.md. GameLift managed-EC2 fleet deployment with alias-flip blue/green and rollback. See plan: docs/superpowers/plans/2026-06-28-deploy-module.md"
```

Then poll CI with `gh pr checks` until green before requesting merge.

---

## Notes for the implementer

- **AWS SDK module:** Task 2 adds `github.com/aws/aws-sdk-go-v2/service/gamelift`. Run `go get` + `go mod tidy` there; the CI build will fail until `go.sum` is committed.
- **Two `var _` interface assertions** guard the GameLift wiring: `var _ fabricac.GameLiftManager = (*awsProvider)(nil)` (Task 2). If a method signature drifts, this fails at compile — fix the method, not the assertion.
- **State `Properties` map** is the existing `ModuleResource.Properties map[string]string` field — fleet `role`/`buildVersion` live there. No new state types.
- **Do not re-register** `AWS::EC2::Instance`, `AWS::EC2::Volume` (perforce), or `AWS::IAM::Role` (ci) in `internal/deploy/cost.go` — `cost.Global.Register` panics on duplicates. Deploy registers only the three `AWS::GameLift::*` types.
- **`testConfig()`** and the `awsProvider` config-loader field name are existing names in the `aws` test package — confirm before writing Task 2's test (noted inline).

