# Horde V1 — Testing Strategy

## Two-Package Pattern (per command)

Every command (`create`, `status`, `submit`) has two test files:

| File | Package | Purpose |
|---|---|---|
| `*_test.go` | `package <cmd>` | White-box: calls `command.run()` directly, injects seams |
| `cobra_test.go` | `package <cmd>_test` | Black-box: calls `New(...) + ExecuteContext`, tests flag parsing and output |

This mirrors the perforce module exactly. See `cmd/perforce/create/create_test.go` and `cobra_test.go` as canonical references.

---

## White-Box Test Seams

Every `command` struct exposes these injectable seams for testing:

```go
readState      func() (*fabricastate.State, error)
writeState     func(*fabricastate.State) error
createResource func(ctx context.Context, r *cloud.Resource) error  // create only
getResource    func(ctx context.Context, r *cloud.Resource) error  // status, submit
probeTCP       func(address string) bool                            // status only
hordeClient    HordeClient                                          // submit only
confirm        func(string, string) bool                            // create only
sleep          func(d time.Duration)                                // status, submit (--wait)
now            func() time.Time                                     // status, submit (--wait timeout)
```

Tests use a `newTestCommand()` helper that pre-wires these with no-op or controllable fakes. AWS provider is never called in unit tests.

---

## `internal/horde` Tests (pure functions, no mocks)

| File | Key cases |
|---|---|
| `plan_test.go` | Missing AmiID error, all defaults applied, VPCResolver called when VPC absent, explicit VPC skips resolver, CostResources populated |
| `resources_test.go` | SGDesiredState: JSON shape, both ports use AllowedCIDR, tags present. InstanceDesiredState: ImageId = AmiID, IMDSv2 enforced, volume present |
| `userdata_test.go` | Password appears in script, `set -euo pipefail` present, empty password errors, readiness sentinel present, `Generate()` returns valid base64 |
| `buildgraph_test.go` | Happy path parses Name/Target, file not found errors, invalid XML errors with path in message, empty BuildGraph succeeds |
| `cost_test.go` | (These live in `internal/perforce/cost_test.go` since m7i prices are added there) |

---

## Create Command — Key Test Cases

| Test | What it verifies |
|---|---|
| `TestCreateDryRunNoAWSCalls` | Zero provider calls on `--dry-run` |
| `TestCreateDryRunOutputFields` | account, region, sg name, instance name, "Cost estimate:" in output |
| `TestCreateAlreadyProvisioned` | Clean exit + "already provisioned" message, zero creates |
| `TestCreateMissingAmiID` | Error contains "horde.amiId is required" and docs/horde-ami.md link |
| `TestCreateHappyPathOrderAndState` | SG created before instance; state written ≥2 times; final state has both resources |
| `TestCreateInstanceFailurePreservesPartialState` | SG identifier in state even when instance fails |
| `TestCreateConfirmationRejected` | Zero creates when confirm returns false |
| `TestCreateNilProvider` | "No infrastructure configured" message, no panic |
| `TestCreateAllowedCIDRWarning` | `"0.0.0.0/0"` in AllowedCIDR → warning in output |
| `TestCreateDryRunDefaultVPCNote` | DefaultVPC=true → "Default VPC used" note |
| `TestCreateDryRunM7i2xlargeRecommendation` | Dry-run output mentions m7i.2xlarge recommendation |
| `TestCreateReadStateError` | Error surfaced before any create call |
| `TestCreateWriteStateError` | Error surfaced after SG creation |

---

## Status Command — Key Test Cases

| Test | What it verifies |
|---|---|
| `TestStatusNotProvisioned` | "not provisioned" message, clean exit |
| `TestStatusProvisioningNoIP` | Shows instance ID, "setting up" message |
| `TestStatusRunningInstanceStateShown` | Instance state label `(running)` in output |
| `TestStatusTCPProbeSuccessTransitionsToReady` | State written as "ready"; output shows "ready" + "responding" |
| `TestStatusProbeAddressFormat` | TCP probe called with `"<ip>:5000"` |
| `TestStatusJSONNotProvisioned` | Valid JSON with `provisioned=false`, `status="not_provisioned"` |
| `TestStatusJSONHordeURLField` | `hordeUrl = "http://<ip>:5000"`, `hordeGrpc = "<ip>:5002"` |
| `TestStatusAlreadyReady` | State not rewritten when already ready |
| `TestStatusWriteStateErrorSurfacedAsWarning` | Write failure → warning in output, run succeeds |
| `TestStatusWaitBecomesReady` | `--wait` exits when probe succeeds |
| `TestStatusWaitTimeout` | `--wait` surfaces "Timed out" when deadline passed |
| `TestStatusGetResourceError` | Error surfaced as command error |
| `TestStatusNoProbeWhenNoPrivateIP` | Probe not attempted when instance has no IP |

---

## Submit Command — Key Test Cases

| Test | What it verifies |
|---|---|
| `TestSubmitBuildGraphParseError` | Error contains "parsing BuildGraph file" |
| `TestSubmitNotProvisioned` | Error contains "not provisioned" |
| `TestSubmitFireAndForget` | `SubmitJob` called once; `GetJobStatus` not called; job ID in output |
| `TestSubmitWaitPollsUntilComplete` | `GetJobStatus` called until "complete"; "complete" in output |
| `TestSubmitWaitTimeout` | "timed out" in output after 60 minutes |
| `TestSubmitClientError` | Connection error surface from `SubmitJob` |

---

## Coverage Target

60%+ on `internal/horde/` (project convention).

Run coverage check:
```bash
go test -coverprofile=coverage.out ./internal/horde/... ./cmd/horde/...
go tool cover -func=coverage.out | grep total
```

---

## Test Helpers Needed

### `fakeProvider` (create white-box tests)

```go
type fakeProvider struct {
    identityErr       error
    sgCreateErr       error
    instanceCreateErr error
    createCalls       int
    createdTypes      []string
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Identity(_ context.Context) (string, string, string, error) {
    if f.identityErr != nil {
        return "", "", "", f.identityErr
    }
    return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (f *fakeProvider) Resources() cloud.ResourceClient {
    return &fakeResourceClient{provider: f}
}

type fakeResourceClient struct{ provider *fakeProvider }

func (r *fakeResourceClient) Create(_ context.Context, res *cloud.Resource) error {
    r.provider.createCalls++
    r.provider.createdTypes = append(r.provider.createdTypes, res.TypeName)
    if res.TypeName == "AWS::EC2::SecurityGroup" && r.provider.sgCreateErr != nil {
        return r.provider.sgCreateErr
    }
    if res.TypeName == "AWS::EC2::Instance" && r.provider.instanceCreateErr != nil {
        return r.provider.instanceCreateErr
    }
    switch res.TypeName {
    case "AWS::EC2::SecurityGroup":
        res.Identifier = fmt.Sprintf("sg-fake%04d", r.provider.createCalls)
    case "AWS::EC2::Instance":
        res.Identifier = fmt.Sprintf("i-fake%04d", r.provider.createCalls)
    }
    return nil
}
func (r *fakeResourceClient) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *fakeResourceClient) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *fakeResourceClient) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *fakeResourceClient) List(_ context.Context, _ string) ([]cloud.Resource, error) {
    return nil, nil
}
```

### `buildTestRoot` (cobra black-box tests)

```go
// For create cobra_test.go
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
    var opts globals.Options
    root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
    root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
    root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
    root.SetOut(out)
    root.SetErr(out)
    optionsSource := func() globals.Options { return opts }
    root.AddCommand(create.New(runtimeSource, optionsSource, out))
    return root
}
```

### `writeStateFile` (cobra black-box tests needing state)

```go
func writeStateFile(dir, content string) error {
    if err := os.MkdirAll(dir+"/.fabrica", 0700); err != nil {
        return err
    }
    return os.WriteFile(dir+"/.fabrica/state.json", []byte(content), 0600)
}
```

### `mustMarshal` (status + submit tests)

```go
func mustMarshal(v any) json.RawMessage {
    data, err := json.Marshal(v)
    if err != nil { panic(err) }
    return data
}
```

### `assertContains`

```go
func assertContains(t *testing.T, s, sub string) {
    t.Helper()
    for i := 0; i <= len(s)-len(sub); i++ {
        if s[i:i+len(sub)] == sub {
            return
        }
    }
    t.Fatalf("%q does not contain %q", s, sub)
}
```
