# Horde V1 — `fabrica horde status` Implementation Plan

## Files

| Action | Path |
|---|---|
| Create | `cmd/horde/status/status.go` |
| Create | `cmd/horde/status/status_test.go` |
| Create | `cmd/horde/status/cobra_test.go` |
| Modify | `cmd/horde/horde.go` (add status subcommand) |

---

## Design

Mirrors `cmd/perforce/status/status.go` exactly, with three differences:
1. Module name is `"horde"` not `"perforce"`
2. TCP probe targets port 5000 (Horde HTTP) not 1666
3. `StatusOutput` has `HordeURL`, `HordeGRPC`, and `HordeStatus` fields instead of `P4PORT` and `HelixCore`

### Readiness probe scope (V1)

We probe port 5000 only — the Horde HTTP API. This is the right signal: if the web UI is serving, MongoDB and Redis are up and Horde is configured. Port 5002 (gRPC) is only needed once agents are connecting; there is nothing in V1 that requires an agent-reachability check. Port 5002 can be added to the probe in a later PR when the agent fleet is introduced.

### Last known state in output

`StatusOutput` intentionally surfaces the last known module status from local state (`"provisioning"` or `"ready"`) even when the live Cloud Control query returns empty `ActualState`. This gives operators a useful debugging baseline: if Cloud Control is returning nothing (stubbed or throttled), the last recorded status and resource IDs are still visible. The `HordeStatus` field (`"responding"` / `"unreachable"` / `"setting up"`) is populated only when a TCP probe was actually attempted.

---

## Task 1: `status.go`

### Step 1: Write failing white-box tests

```go
// cmd/horde/status/status_test.go
package status

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "testing"
    "time"

    "github.com/jpvelasco/fabrica/cmd/globals"
    "github.com/jpvelasco/fabrica/internal/cloud"
    "github.com/jpvelasco/fabrica/internal/config"
    fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func newTestCommand(out *bytes.Buffer, st *fabricastate.State, getResource func(context.Context, *cloud.Resource) error, probe func(string) bool) command {
    cfg := config.Defaults()
    c := command{
        runtime: globals.Runtime{Config: cfg, Provider: nil},
        out:     out,
        sleep:   func(time.Duration) {},
        now:     time.Now,
    }
    c.readState = func() (*fabricastate.State, error) { return st, nil }
    c.writeState = func(_ *fabricastate.State) error { return nil }
    c.getResource = getResource
    if probe != nil {
        c.probeTCP = probe
    } else {
        c.probeTCP = func(string) bool { return false }
    }
    return c
}

func hordeState(status string, withInstance bool) *fabricastate.State {
    st := fabricastate.NewState("123456789012", "us-east-1")
    resources := []fabricastate.ModuleResource{
        {TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-horde123"},
    }
    if withInstance {
        resources = append(resources, fabricastate.ModuleResource{
            TypeName:   "AWS::EC2::Instance",
            Identifier: "i-horde123",
        })
    }
    st.UpsertModule("horde", "", status, resources)
    return st
}

func mustMarshal(v any) json.RawMessage {
    data, err := json.Marshal(v)
    if err != nil { panic(err) }
    return data
}

// TestStatusNotProvisioned verifies clean output when no module in state.
func TestStatusNotProvisioned(t *testing.T) {
    var out bytes.Buffer
    st := fabricastate.NewState("123456789012", "us-east-1")
    c := newTestCommand(&out, st, nil, nil)
    if err := c.run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    assertContains(t, out.String(), "not provisioned")
}

// TestStatusProvisioningNoIP verifies output when instance has no IP yet.
func TestStatusProvisioningNoIP(t *testing.T) {
    var out bytes.Buffer
    st := hordeState("provisioning", true)
    c := newTestCommand(&out, st, func(_ context.Context, r *cloud.Resource) error { return nil }, nil)
    if err := c.run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    assertContains(t, out.String(), "provisioning")
    assertContains(t, out.String(), "setting up")
}

// TestStatusTCPProbeSuccessTransitionsToReady verifies state updated and output shows ready.
func TestStatusTCPProbeSuccessTransitionsToReady(t *testing.T) {
    var out bytes.Buffer
    st := hordeState("provisioning", true)
    var writtenStatus string
    getResource := func(_ context.Context, r *cloud.Resource) error {
        r.ActualState = mustMarshal(map[string]any{
            "InstanceType": "m7i.xlarge",
            "PrivateIpAddress": "10.0.1.42",
            "State": map[string]any{"Name": "running"},
        })
        return nil
    }
    c := newTestCommand(&out, st, getResource, func(string) bool { return true })
    c.writeState = func(s *fabricastate.State) error {
        if m := s.GetModule("horde"); m != nil {
            writtenStatus = m.Status
        }
        return nil
    }
    if err := c.run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    assertContains(t, out.String(), "ready")
    assertContains(t, out.String(), "responding")
    if writtenStatus != "ready" {
        t.Errorf("state written with status %q, want ready", writtenStatus)
    }
}

// TestStatusProbeAddressFormat verifies probe is called with "ip:5000".
func TestStatusProbeAddressFormat(t *testing.T) {
    var out bytes.Buffer
    st := hordeState("provisioning", true)
    var probeAddr string
    getResource := func(_ context.Context, r *cloud.Resource) error {
        r.ActualState = mustMarshal(map[string]any{
            "PrivateIpAddress": "192.168.1.10",
            "State": map[string]any{"Name": "running"},
        })
        return nil
    }
    c := newTestCommand(&out, st, getResource, func(addr string) bool {
        probeAddr = addr
        return false
    })
    if err := c.run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    if probeAddr != "192.168.1.10:5000" {
        t.Errorf("probe address = %q, want 192.168.1.10:5000", probeAddr)
    }
}

// TestStatusJSONNotProvisioned verifies JSON output when not provisioned.
func TestStatusJSONNotProvisioned(t *testing.T) {
    var out bytes.Buffer
    st := fabricastate.NewState("123456789012", "us-east-1")
    c := newTestCommand(&out, st, nil, nil)
    c.jsonOut = true
    if err := c.run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    var result StatusOutput
    if err := json.Unmarshal(out.Bytes(), &result); err != nil {
        t.Fatalf("invalid JSON: %v", err)
    }
    if result.Provisioned {
        t.Error("expected provisioned=false")
    }
    if result.Status != "not_provisioned" {
        t.Errorf("status = %q, want not_provisioned", result.Status)
    }
}

// TestStatusJSONHordeURLField verifies hordeUrl and hordeGrpc fields.
func TestStatusJSONHordeURLField(t *testing.T) {
    var out bytes.Buffer
    st := hordeState("ready", true)
    getResource := func(_ context.Context, r *cloud.Resource) error {
        r.ActualState = mustMarshal(map[string]any{
            "InstanceType": "m7i.xlarge",
            "PrivateIpAddress": "10.0.1.42",
            "State": map[string]any{"Name": "running"},
        })
        return nil
    }
    c := newTestCommand(&out, st, getResource, func(string) bool { return true })
    c.jsonOut = true
    if err := c.run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    var result StatusOutput
    if err := json.Unmarshal(out.Bytes(), &result); err != nil {
        t.Fatalf("invalid JSON: %v", err)
    }
    if result.HordeURL != "http://10.0.1.42:5000" {
        t.Errorf("hordeUrl = %q, want http://10.0.1.42:5000", result.HordeURL)
    }
    if result.HordeGRPC != "10.0.1.42:5002" {
        t.Errorf("hordeGrpc = %q, want 10.0.1.42:5002", result.HordeGRPC)
    }
}

// TestStatusWaitBecomesReady verifies --wait exits on successful probe.
func TestStatusWaitBecomesReady(t *testing.T) {
    var out bytes.Buffer
    st := hordeState("provisioning", true)
    probeCall := 0
    getResource := func(_ context.Context, r *cloud.Resource) error {
        r.ActualState = mustMarshal(map[string]any{
            "PrivateIpAddress": "10.0.1.42",
            "State": map[string]any{"Name": "running"},
        })
        return nil
    }
    c := newTestCommand(&out, st, getResource, func(string) bool {
        probeCall++
        return probeCall >= 2
    })
    c.wait = true
    if err := c.run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    assertContains(t, out.String(), "ready")
}

// TestStatusWaitTimeout verifies --wait surfaces timeout message.
func TestStatusWaitTimeout(t *testing.T) {
    var out bytes.Buffer
    st := hordeState("provisioning", true)
    getResource := func(_ context.Context, r *cloud.Resource) error {
        r.ActualState = mustMarshal(map[string]any{
            "PrivateIpAddress": "10.0.1.42",
            "State": map[string]any{"Name": "running"},
        })
        return nil
    }
    startTime := time.Now()
    callCount := 0
    c := newTestCommand(&out, st, getResource, func(string) bool { return false })
    c.wait = true
    c.now = func() time.Time {
        callCount++
        if callCount <= 1 {
            return startTime
        }
        return startTime.Add(waitDeadline + time.Second)
    }
    if err := c.run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    assertContains(t, out.String(), "Timed out")
}

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

### Step 2: Create `cmd/horde/status/status.go`

Key types and constants:

```go
package status

const (
    lineWidth    = 58
    moduleName   = "horde"
    probeTimeout = 3 * time.Second
    waitInterval = 15 * time.Second
    waitDeadline = 10 * time.Minute
)

type statusInfo struct {
    moduleStatus  string
    instanceID    string
    sgID          string
    instanceType  string
    privateIP     string
    instanceState string
    port          int
    grpcPort      int

    hordeReachable      bool
    hordeProbeAttempted bool
}

type StatusOutput struct {
    Provisioned  bool   `json:"provisioned"`
    Status       string `json:"status"`
    InstanceID   string `json:"instanceId,omitempty"`
    SGID         string `json:"sgId,omitempty"`
    InstanceType string `json:"instanceType,omitempty"`
    PrivateIP    string `json:"privateIp,omitempty"`
    HordeURL     string `json:"hordeUrl,omitempty"`
    HordeGRPC    string `json:"hordeGrpc,omitempty"`
    HordeStatus  string `json:"hordeStatus,omitempty"` // "responding" | "unreachable" | "setting up"
}

type command struct {
    runtime globals.Runtime
    jsonOut bool
    wait    bool
    out     io.Writer

    readState   func() (*fabricastate.State, error)
    writeState  func(*fabricastate.State) error
    getResource func(ctx context.Context, r *cloud.Resource) error
    probeTCP    func(address string) bool
    sleep       func(d time.Duration)
    now         func() time.Time
}
```

**Port values** come from state/config. Since the port is baked into the cloud-init at create time and there is no guarantee of a config file at status time, the status command reads the port from the `HordeConfig` in runtime (defaulting to 5000/5002 if zero). This is the same pattern used for VolumeSize in destroy.

**`buildInfo()` probe address:** `fmt.Sprintf("%s:%d", info.privateIP, port)` where port comes from `c.runtime.Config.Horde.Port` (defaulting to 5000 if zero).

**`HordeURL` and `HordeGRPC`** are constructed as:
- `HordeURL = fmt.Sprintf("http://%s:%d", privateIP, port)`
- `HordeGRPC = fmt.Sprintf("%s:%d", privateIP, grpcPort)`

TCP probe targets port 5000 (Horde HTTP). The transition `provisioning → ready` fires on first successful probe, same as the perforce pattern.

### Step 3: Add status to `cmd/horde/horde.go`

```go
cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
```

### Step 4: Run tests

Run: `go test ./cmd/horde/status/...`
Expected: PASS

### Step 5: Commit

```bash
git add cmd/horde/status/ cmd/horde/horde.go
git commit -m "feat: add fabrica horde status command"
```

---

## Cobra-layer tests (`cobra_test.go`)

Follow the same pattern as `cmd/perforce/status/cobra_test.go`:

```go
// Key test cases for cmd/horde/status/cobra_test.go (package status_test)
func TestStatusCobraNotProvisioned(t *testing.T)   // no state → "not provisioned"
func TestStatusCobraJSONFlag(t *testing.T)          // --json → parseable JSON
func TestStatusCobraNilProvider(t *testing.T)       // nil provider → no panic
func TestStatusCobraRuntimeError(t *testing.T)      // runtimeSource error → command error
func TestStatusCobraWaitFlagAccepted(t *testing.T)  // --wait/-w accepted, no parse error
func TestStatusCobraJSONProvisioned(t *testing.T)   // state on disk → provisioned=true in JSON
```

`writeStateFile()` helper writes `.fabrica/state.json` with `"horde"` module:
```go
stateJSON := `{"account":"123456789012","region":"us-east-1","modules":[
    {"name":"horde","version":"","status":"provisioning","resources":[
        {"typeName":"AWS::EC2::SecurityGroup","identifier":"sg-horde"},
        {"typeName":"AWS::EC2::Instance","identifier":"i-horde"}
    ]}]}`
```
