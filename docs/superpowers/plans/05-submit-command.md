# Horde V1 — `fabrica horde submit` Implementation Plan

## Files

| Action | Path |
|---|---|
| Create | `internal/horde/buildgraph/buildgraph.go` |
| Create | `internal/horde/buildgraph/buildgraph_test.go` |
| Create | `cmd/horde/submit/client.go` |
| Create | `cmd/horde/submit/submit.go` |
| Create | `cmd/horde/submit/submit_test.go` |
| Create | `cmd/horde/submit/cobra_test.go` |
| Modify | `cmd/horde/horde.go` (add submit subcommand) |

---

## Task 1: `internal/horde/buildgraph/buildgraph.go`

Pure XML parsing — no AWS, no HTTP. Lives in its own sub-package (`buildgraph`) so it can be imported by any future command without pulling in the full `internal/horde` plan layer.

### Error handling for malformed files

`ParseBuildGraph` returns a wrapped error in two cases:
- File unreadable: `"reading BuildGraph file %s: %w"` — includes the path so the user knows which file failed.
- XML malformed: `"parsing BuildGraph file %s: %w"` — same path-in-message convention. Callers (`submit.go`) re-wrap this as a command error; the original parse error is always visible in the chain.

Empty `<BuildGraph>` (no agents, no nodes) is **not** an error — it produces a zero-value `BuildGraphJob`. The submit command checks `job.Name == ""` and may warn, but does not abort.

### Step 1: Write failing tests

Create a minimal valid BuildGraph XML file for tests:

```go
// internal/horde/buildgraph/buildgraph_test.go
package buildgraph

import (
    "os"
    "path/filepath"
    "testing"
)

const validBuildGraphXML = `<?xml version="1.0" encoding="utf-8"?>
<BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
    <Agent Name="BuildAgent" Type="Win64">
        <Node Name="Compile Editor Win64">
            <Property Name="Target" Value="UnrealEditor"/>
        </Node>
    </Agent>
</BuildGraph>`

func writeTempBuildGraph(t *testing.T, content string) string {
    t.Helper()
    dir := t.TempDir()
    path := filepath.Join(dir, "BuildGraph.xml")
    if err := os.WriteFile(path, []byte(content), 0644); err != nil {
        t.Fatal(err)
    }
    return path
}

func TestParseBuildGraphHappyPath(t *testing.T) {
    path := writeTempBuildGraph(t, validBuildGraphXML)
    job, err := ParseBuildGraph(path)
    if err != nil {
        t.Fatalf("ParseBuildGraph: %v", err)
    }
    if job == nil {
        t.Fatal("expected non-nil BuildGraphJob")
    }
    if job.Name == "" {
        t.Error("job.Name should not be empty")
    }
}

func TestParseBuildGraphFileNotFound(t *testing.T) {
    _, err := ParseBuildGraph("/nonexistent/path/BuildGraph.xml")
    if err == nil {
        t.Fatal("expected error for missing file")
    }
}

func TestParseBuildGraphInvalidXML(t *testing.T) {
    path := writeTempBuildGraph(t, "<not valid xml><<</")
    _, err := ParseBuildGraph(path)
    if err == nil {
        t.Fatal("expected error for invalid XML")
    }
    assertContains(t, err.Error(), "parsing BuildGraph file")
}

func TestParseBuildGraphEmptyGraph(t *testing.T) {
    path := writeTempBuildGraph(t, `<?xml version="1.0"?><BuildGraph xmlns="http://www.epicgames.com/BuildGraph"></BuildGraph>`)
    // Should succeed with an empty job (no agents/nodes)
    _, err := ParseBuildGraph(path)
    if err != nil {
        t.Fatalf("empty BuildGraph should not error: %v", err)
    }
}
```

Run: `go test ./internal/horde/buildgraph/... -run TestParseBuildGraph`
Expected: FAIL (package doesn't exist yet)

### Step 2: Create `internal/horde/buildgraph/buildgraph.go`

```go
package buildgraph

import (
    "encoding/xml"
    "fmt"
    "os"
)

// BuildGraphJob holds the parsed essentials from a BuildGraph XML file.
type BuildGraphJob struct {
    Name   string
    Target string
}

type buildGraphXML struct {
    XMLName xml.Name      `xml:"BuildGraph"`
    Agents  []bgAgentXML  `xml:"Agent"`
}

type bgAgentXML struct {
    Name  string      `xml:"Name,attr"`
    Nodes []bgNodeXML `xml:"Node"`
}

type bgNodeXML struct {
    Name string `xml:"Name,attr"`
}

// ParseBuildGraph reads path, parses the BuildGraph XML, and returns a
// BuildGraphJob. Returns an error if the file cannot be read or the XML
// is malformed.
func ParseBuildGraph(path string) (*BuildGraphJob, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("reading BuildGraph file %s: %w", path, err)
    }
    var bg buildGraphXML
    if err := xml.Unmarshal(data, &bg); err != nil {
        return nil, fmt.Errorf("parsing BuildGraph file %s: %w", path, err)
    }
    job := &BuildGraphJob{}
    if len(bg.Agents) > 0 {
        job.Name = bg.Agents[0].Name
        if len(bg.Agents[0].Nodes) > 0 {
            job.Target = bg.Agents[0].Nodes[0].Name
        }
    }
    return job, nil
}
```

### Step 3: Run tests and commit

Run: `go test ./internal/horde/buildgraph/... -run TestParseBuildGraph`
Expected: PASS

```bash
git add internal/horde/buildgraph/
git commit -m "feat: add BuildGraph XML parser"
```

---

## Task 2: `cmd/horde/submit/client.go`

### HordeClient interface

```go
// cmd/horde/submit/client.go
package submit

import (
    "context"
    "github.com/jpvelasco/fabrica/internal/horde/buildgraph"
)

// HordeClient abstracts communication with the Horde REST API.
// The interface lives here (not in internal/) because only submit needs it in V1.
type HordeClient interface {
    SubmitJob(ctx context.Context, job *buildgraph.BuildGraphJob) (jobID string, err error)
    GetJobStatus(ctx context.Context, jobID string) (state string, err error)
}
```

### Concrete implementation

```go
type hordeHTTPClient struct {
    baseURL string  // e.g. "http://10.0.1.42:5000"
    token   string  // service account token from .fabrica/horde-credentials.yaml
}

func newHordeHTTPClient(baseURL, token string) *hordeHTTPClient {
    return &hordeHTTPClient{baseURL: baseURL, token: token}
}

func (c *hordeHTTPClient) SubmitJob(ctx context.Context, job *horde.BuildGraphJob) (string, error) {
    // POST /api/v1/jobs
    // Body: {"name": job.Name, "target": job.Target}
    // Returns: {"id": "..."}
    // Auth header: "Authorization: ServiceAccount <token>"
    // ...
}

func (c *hordeHTTPClient) GetJobStatus(ctx context.Context, jobID string) (string, error) {
    // GET /api/v1/jobs/{jobID}
    // Returns: {"state": "..."}  ("waiting", "running", "complete", "error")
    // ...
}
```

**URL resolution:** `newHordeHTTPClient` is called from `submit.go`. The caller reads the instance identifier from state, calls `provider.Resources().Get` (Cloud Control) to retrieve the `PrivateIpAddress`, and constructs `http://<ip>:<port>`. If state has no instance identifier, returns the error: `"Horde is not provisioned. Run 'fabrica horde create' first."`

**Token:** Read from `.fabrica/horde-credentials.yaml`. The `horde_service_token` field. If absent, the field is empty string and the auth header is still sent (Horde returns 401 if invalid).

---

## Task 3: `cmd/horde/submit/submit.go`

### Step 1: Write failing white-box tests

```go
// cmd/horde/submit/submit_test.go
package submit

import (
    "bytes"
    "context"
    "errors"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/jpvelasco/fabrica/cmd/globals"
    "github.com/jpvelasco/fabrica/internal/config"
    "github.com/jpvelasco/fabrica/internal/horde/buildgraph"
    fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// fakeHordeClient is a controllable HordeClient for testing.
type fakeHordeClient struct {
    submitErr   error
    submitJobID string
    statusMap   map[string]string // jobID → state
    statusErr   error
    submitCalls int
    statusCalls int
}

func (f *fakeHordeClient) SubmitJob(_ context.Context, _ *buildgraph.BuildGraphJob) (string, error) {
    f.submitCalls++
    if f.submitErr != nil {
        return "", f.submitErr
    }
    if f.submitJobID == "" {
        return "job-fake-001", nil
    }
    return f.submitJobID, nil
}

func (f *fakeHordeClient) GetJobStatus(_ context.Context, jobID string) (string, error) {
    f.statusCalls++
    if f.statusErr != nil {
        return "", f.statusErr
    }
    if s, ok := f.statusMap[jobID]; ok {
        return s, nil
    }
    return "waiting", nil
}

func writeTempBuildGraph(t *testing.T) string {
    t.Helper()
    dir := t.TempDir()
    path := filepath.Join(dir, "BuildGraph.xml")
    xml := `<?xml version="1.0"?><BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
        <Agent Name="BuildAgent" Type="Win64"><Node Name="Compile"/></Agent>
    </BuildGraph>`
    if err := os.WriteFile(path, []byte(xml), 0644); err != nil {
        t.Fatal(err)
    }
    return path
}

func newTestCommand(out *bytes.Buffer, client HordeClient, st *fabricastate.State) command {
    cfg := config.Defaults()
    c := command{
        runtime:     globals.Runtime{Config: cfg, Provider: nil},
        out:         out,
        hordeClient: client,
        sleep:       func(time.Duration) {},
        now:         time.Now,
    }
    c.readState = func() (*fabricastate.State, error) { return st, nil }
    return c
}

func hordeProvisionedState() *fabricastate.State {
    st := fabricastate.NewState("123456789012", "us-east-1")
    st.UpsertModule("horde", "", "ready", []fabricastate.ModuleResource{
        {TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-horde123"},
        {TypeName: "AWS::EC2::Instance", Identifier: "i-horde123"},
    })
    return st
}

// TestSubmitBuildGraphParseError verifies error when BuildGraph file is invalid.
func TestSubmitBuildGraphParseError(t *testing.T) {
    var out bytes.Buffer
    dir := t.TempDir()
    badXML := filepath.Join(dir, "bad.xml")
    os.WriteFile(badXML, []byte("not xml"), 0644)

    st := hordeProvisionedState()
    c := newTestCommand(&out, &fakeHordeClient{}, st)
    c.buildGraphPath = badXML

    err := c.run(context.Background())
    if err == nil {
        t.Fatal("expected error for invalid XML")
    }
    assertContains(t, err.Error(), "parsing BuildGraph file")
}

// TestSubmitNotProvisioned verifies error when no horde module in state.
func TestSubmitNotProvisioned(t *testing.T) {
    var out bytes.Buffer
    st := fabricastate.NewState("123456789012", "us-east-1")
    c := newTestCommand(&out, &fakeHordeClient{}, st)
    c.buildGraphPath = writeTempBuildGraph(t)

    err := c.run(context.Background())
    if err == nil {
        t.Fatal("expected error when horde not provisioned")
    }
    assertContains(t, err.Error(), "not provisioned")
}

// TestSubmitFireAndForget verifies successful submit without --wait.
func TestSubmitFireAndForget(t *testing.T) {
    var out bytes.Buffer
    st := hordeProvisionedState()
    client := &fakeHordeClient{submitJobID: "job-abc-001"}
    c := newTestCommand(&out, client, st)
    c.buildGraphPath = writeTempBuildGraph(t)

    if err := c.run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    if client.submitCalls != 1 {
        t.Errorf("submitCalls = %d, want 1", client.submitCalls)
    }
    if client.statusCalls != 0 {
        t.Errorf("statusCalls = %d, want 0 (fire-and-forget)", client.statusCalls)
    }
    assertContains(t, out.String(), "job-abc-001")
}

// TestSubmitWaitPollsUntilComplete verifies --wait polls GetJobStatus until complete.
func TestSubmitWaitPollsUntilComplete(t *testing.T) {
    var out bytes.Buffer
    st := hordeProvisionedState()
    callCount := 0
    client := &fakeHordeClient{
        submitJobID: "job-wait-001",
        statusMap:   map[string]string{},
    }
    // Return "waiting" first 2 calls, then "complete"
    origGetStatus := client
    _ = origGetStatus
    c := newTestCommand(&out, client, st)
    c.buildGraphPath = writeTempBuildGraph(t)
    c.wait = true
    c.hordeClient = &fakeHordeClient{
        submitJobID: "job-wait-001",
        statusMap: map[string]string{
            "job-wait-001": "waiting",
        },
    }
    // Override GetJobStatus to progress
    statusSeq := []string{"waiting", "running", "complete"}
    c.hordeClient = &sequentialFakeClient{
        submitJobID: "job-wait-001",
        statusSeq:   statusSeq,
        idx:         &callCount,
    }

    if err := c.run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    assertContains(t, out.String(), "complete")
}

// TestSubmitWaitTimeout verifies --wait surfaces timeout after 60 minutes.
func TestSubmitWaitTimeout(t *testing.T) {
    var out bytes.Buffer
    st := hordeProvisionedState()
    c := newTestCommand(&out, &fakeHordeClient{submitJobID: "job-timeout"}, st)
    c.buildGraphPath = writeTempBuildGraph(t)
    c.wait = true
    startTime := time.Now()
    callCount := 0
    c.now = func() time.Time {
        callCount++
        if callCount <= 1 {
            return startTime
        }
        return startTime.Add(waitTimeout + time.Second)
    }

    if err := c.run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    assertContains(t, out.String(), "timed out")
}

// TestSubmitClientError verifies connection error message format.
func TestSubmitClientError(t *testing.T) {
    var out bytes.Buffer
    st := hordeProvisionedState()
    c := newTestCommand(&out, &fakeHordeClient{
        submitErr: errors.New("connection refused"),
    }, st)
    c.buildGraphPath = writeTempBuildGraph(t)

    err := c.run(context.Background())
    if err == nil {
        t.Fatal("expected error on client failure")
    }
}

// sequentialFakeClient returns status values in sequence.
type sequentialFakeClient struct {
    submitJobID string
    statusSeq   []string
    idx         *int
}

func (f *sequentialFakeClient) SubmitJob(_ context.Context, _ *buildgraph.BuildGraphJob) (string, error) {
    return f.submitJobID, nil
}

func (f *sequentialFakeClient) GetJobStatus(_ context.Context, _ string) (string, error) {
    i := *f.idx
    *f.idx++
    if i >= len(f.statusSeq) {
        return f.statusSeq[len(f.statusSeq)-1], nil
    }
    return f.statusSeq[i], nil
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

### Step 2: Create `cmd/horde/submit/submit.go`

Key constants and types:

```go
package submit

const (
    moduleName   = "horde"
    waitInterval = 30 * time.Second
    waitTimeout  = 60 * time.Minute
)

type command struct {
    runtime        globals.Runtime
    buildGraphPath string
    wait           bool
    out            io.Writer

    readState   func() (*fabricastate.State, error)
    hordeClient HordeClient
    sleep       func(time.Duration)
    now         func() time.Time
}
```

`New()` flags: positional arg `<buildgraph-file>`, `--wait`/`-w` bool flag.

`run()` flow:
1. Parse BuildGraph file: `buildgraph.ParseBuildGraph(c.buildGraphPath)`
2. Read state; check horde module exists — if not, error `"Horde is not provisioned. Run 'fabrica horde create' first."`
3. If `c.hordeClient == nil`: resolve private IP via `getResource` (Cloud Control Get on instance), construct `hordeHTTPClient`
4. Call `hordeClient.SubmitJob(ctx, job)` → get `jobID`
5. Print `"Job submitted: <jobID>"`
6. If `--wait`: poll `GetJobStatus` every 30s until state is `"complete"` or `"error"` (or 60min timeout)
7. Handle Ctrl-C: print "Job ID: <id> — monitor in Horde web UI"

**Error messages:**
- Connection refused: `"connecting to Horde at %s: connection refused. Is the coordinator running? Check: fabrica horde status"`
- HTTP 401/403: `"Horde rejected the request (auth): check admin token in .fabrica/horde-credentials.yaml"`
- `--wait` timeout: `"timed out waiting for job %s to complete (60 minutes)"`

### Step 3: Wire submit into horde.go and run tests

```go
// Add to cmd/horde/horde.go
cmd.AddCommand(submit.New(runtimeSource, optionsSource, out))
```

Run: `go test ./cmd/horde/submit/... && go test ./internal/horde/... ./internal/horde/buildgraph/...`
Expected: PASS

### Step 4: Commit

```bash
git add cmd/horde/submit/ cmd/horde/horde.go
git commit -m "feat: add fabrica horde submit command"
```

---

## Cobra-layer tests (`cobra_test.go`)

```go
// Key test cases for cmd/horde/submit/cobra_test.go (package submit_test)
func TestSubmitCobraMissingArg(t *testing.T)       // no positional arg → usage error
func TestSubmitCobraWaitFlagAccepted(t *testing.T) // --wait/-w accepted, no parse error
func TestSubmitCobraNilProvider(t *testing.T)      // nil provider → "not provisioned" (no state)
func TestSubmitCobraRuntimeError(t *testing.T)     // runtimeSource error → command error
```
