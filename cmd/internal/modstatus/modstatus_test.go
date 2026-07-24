package modstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// recordingRenderer captures what the engine renders, so orchestration tests
// assert on engine behavior (the Info produced, ready transitions) rather than
// on any module's specific output strings.
type recordingRenderer struct {
	notProvisionedCalls int
	results             []Info
}

func (r *recordingRenderer) NotProvisioned(out io.Writer, jsonOut bool) {
	r.notProvisionedCalls++
	fmt.Fprintln(out, "not provisioned")
}

func (r *recordingRenderer) Result(out io.Writer, info Info, jsonOut bool) {
	r.results = append(r.results, info)
	fmt.Fprintf(out, "status=%s reachable=%v\n", info.ModuleStatus, info.Reachable)
}

func (r *recordingRenderer) last() Info {
	return r.results[len(r.results)-1]
}

// newTestCommand builds an engine command with injected state and seams,
// using a recording renderer and probe port 1666.
func newTestCommand(out *bytes.Buffer, st *fabricastate.State, rr *recordingRenderer, getResource func(context.Context, *cloud.Resource) error, probe func(string) bool) Command {
	cfg := config.Defaults()
	c := Command{
		Spec:     Spec{ModuleName: "perforce", ProbePort: 1666, DisplayName: "Perforce"},
		Renderer: rr,
		Runtime:  globals.Runtime{Config: cfg},
		Out:      out,
		Sleep:    func(time.Duration) {},
		Now:      time.Now,
	}
	c.ReadState = func() (*fabricastate.State, error) { return st, nil }
	c.WriteState = func(_ *fabricastate.State) error { return nil }
	c.GetResource = getResource
	if probe != nil {
		c.ProbeTCP = probe
	} else {
		c.ProbeTCP = func(string) bool { return false }
	}
	return c
}

func moduleState(status string, withInstance bool) *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	resources := []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc123"},
	}
	if withInstance {
		resources = append(resources, fabricastate.ModuleResource{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-abc123",
		})
	}
	st.UpsertModule("perforce", "2024.2", status, resources)
	return st
}

func TestBuildInfo_LastBackupProperties(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-1"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-1", Properties: map[string]string{
			"lastBackupId": "bid",
			"lastBackupAt": "bat",
		}},
	})
	var out bytes.Buffer
	rr := &recordingRenderer{}
	c := newTestCommand(&out, st, rr, runningInstance, func(string) bool { return true })
	if err := c.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	info := rr.last()
	if info.LastBackupId != "bid" || info.LastBackupAt != "bat" {
		t.Fatalf("last backup fields: %+v", info)
	}
}

func runningInstance(_ context.Context, r *cloud.Resource) error {
	r.ActualState = mustMarshal(map[string]any{
		"InstanceType":     "m5.xlarge",
		"PrivateIpAddress": "10.0.1.42",
		"State":            map[string]any{"Name": "running"},
	})
	return nil
}

func TestRunNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st, rr, nil, nil)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rr.notProvisionedCalls != 1 {
		t.Errorf("NotProvisioned called %d times, want 1", rr.notProvisionedCalls)
	}
}

func TestRunPopulatesInfoFromState(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, rr, runningInstance, func(string) bool { return false })

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := rr.last()
	if got.InstanceID != "i-abc123" {
		t.Errorf("InstanceID = %q", got.InstanceID)
	}
	if got.SGID != "sg-abc123" {
		t.Errorf("SGID = %q", got.SGID)
	}
	if got.InstanceType != "m5.xlarge" {
		t.Errorf("InstanceType = %q", got.InstanceType)
	}
	if got.PrivateIP != "10.0.1.42" {
		t.Errorf("PrivateIP = %q", got.PrivateIP)
	}
	if got.InstanceState != "running" {
		t.Errorf("InstanceState = %q", got.InstanceState)
	}
}

func TestRunProbeSuccessTransitionsToReady(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := moduleState("provisioning", true)
	var writtenStatus string
	c := newTestCommand(&out, st, rr, runningInstance, func(string) bool { return true })
	c.WriteState = func(s *fabricastate.State) error {
		if m := s.GetModule("perforce"); m != nil {
			writtenStatus = m.Status
		}
		return nil
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rr.last(); got.ModuleStatus != "ready" || !got.Reachable {
		t.Errorf("info = %+v, want ready+reachable", got)
	}
	if writtenStatus != "ready" {
		t.Errorf("state written with status %q, want ready", writtenStatus)
	}
}

func TestRunAlreadyReadyNoStateWrite(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := moduleState("ready", true)
	stateWritten := false
	c := newTestCommand(&out, st, rr, runningInstance, func(string) bool { return true })
	c.WriteState = func(_ *fabricastate.State) error {
		stateWritten = true
		return nil
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stateWritten {
		t.Error("state must not be rewritten when module is already ready")
	}
}

func TestRunGetResourceError(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, rr, func(_ context.Context, _ *cloud.Resource) error {
		return errors.New("describe failed")
	}, nil)

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when GetResource fails")
	}
	assert.Contains(t, err.Error(), "querying instance")
}

func TestRunOnlySGNoInstance(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := moduleState("provisioning", false) // SG only
	c := newTestCommand(&out, st, rr, nil, nil)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := rr.last()
	if got.InstanceID != "" {
		t.Errorf("InstanceID = %q, want empty", got.InstanceID)
	}
	if got.SGID != "sg-abc123" {
		t.Errorf("SGID = %q", got.SGID)
	}
}

func TestRunReadStateError(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	c := newTestCommand(&out, nil, rr, nil, nil)
	c.ReadState = func() (*fabricastate.State, error) {
		return nil, errors.New("disk failure")
	}

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when ReadState fails")
	}
	assert.Contains(t, err.Error(), "reading state")
}

func TestRunWriteStateErrorSurfacedAsWarning(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, rr, runningInstance, func(string) bool { return true })
	c.WriteState = func(_ *fabricastate.State) error {
		return errors.New("disk full")
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("must not return error on WriteState failure: %v", err)
	}
	assert.Contains(t, out.String(), "Warning")
	assert.Contains(t, out.String(), "disk full")
}

func TestRunProbeAddressUsesSpecPort(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := moduleState("provisioning", true)
	var probeAddr string
	c := newTestCommand(&out, st, rr, func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m5.xlarge",
			"PrivateIpAddress": "192.168.1.10",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}, func(addr string) bool {
		probeAddr = addr
		return false
	})

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if probeAddr != "192.168.1.10:1666" {
		t.Errorf("probe address = %q, want 192.168.1.10:1666", probeAddr)
	}
}

func TestRunNoProbeWhenNoPrivateIP(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := moduleState("provisioning", true)
	probeCalled := false
	c := newTestCommand(&out, st, rr, func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType": "m5.xlarge",
			"State":        map[string]any{"Name": "pending"},
		})
		return nil
	}, func(string) bool {
		probeCalled = true
		return true
	})

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if probeCalled {
		t.Error("TCP probe must not be attempted when instance has no private IP")
	}
	if got := rr.last(); got.ProbeAttempted {
		t.Error("ProbeAttempted must be false without a private IP")
	}
}

func TestRunWaitBecomesReady(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := moduleState("provisioning", true)
	probeCall := 0
	c := newTestCommand(&out, st, rr, runningInstance, func(string) bool {
		probeCall++
		return probeCall >= 2 // fail once, succeed on second call
	})
	c.Wait = true

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rr.last(); got.ModuleStatus != "ready" || !got.Reachable {
		t.Errorf("final info = %+v, want ready+reachable", got)
	}
	if probeCall < 2 {
		t.Errorf("expected at least 2 probe calls, got %d", probeCall)
	}
}

func TestRunWaitTimeout(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := moduleState("provisioning", true)
	startTime := time.Now()
	callCount := 0
	c := newTestCommand(&out, st, rr, runningInstance, func(string) bool { return false })
	c.Wait = true
	c.Now = func() time.Time {
		callCount++
		if callCount <= 1 {
			return startTime // first call computes the deadline
		}
		return startTime.Add(waitDeadline + time.Second) // subsequent: past deadline
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assert.Contains(t, out.String(), "Timed out")
	assert.Contains(t, out.String(), "Perforce") // DisplayName in the timeout message
}

func TestRunWaitGetResourceError(t *testing.T) {
	var out bytes.Buffer
	rr := &recordingRenderer{}
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, rr, func(_ context.Context, _ *cloud.Resource) error {
		return errors.New("network timeout")
	}, nil)
	c.Wait = true

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when GetResource fails during wait")
	}
	assert.Contains(t, err.Error(), "querying instance")
}

func TestParseInstanceActualStateIgnoresEmpty(t *testing.T) {
	info := Info{}
	parseInstanceActualState(&cloud.Resource{}, &info) // nil ActualState
	if info.InstanceType != "" || info.PrivateIP != "" {
		t.Errorf("empty ActualState must leave info untouched: %+v", info)
	}
	parseInstanceActualState(&cloud.Resource{ActualState: []byte("not json")}, &info)
	if info.InstanceType != "" {
		t.Errorf("unparseable ActualState must be ignored: %+v", info)
	}
}

// ---- helpers ----

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// ---- tests for shared helpers ----

func TestWriteCommonFields(t *testing.T) {
	info := Info{
		InstanceID:    "i-abc123",
		InstanceState: "running",
		InstanceType:  "m5.xlarge",
		PrivateIP:     "10.0.1.42",
	}
	var buf bytes.Buffer
	WriteCommonFields(&buf, info)
	out := buf.String()
	if !strings.Contains(out, "i-abc123") {
		t.Error("expected InstanceID in output")
	}
	if !strings.Contains(out, "running") {
		t.Error("expected InstanceState in output")
	}
	if !strings.Contains(out, "m5.xlarge") {
		t.Error("expected InstanceType in output")
	}
	if !strings.Contains(out, "10.0.1.42") {
		t.Error("expected PrivateIP in output")
	}
}

func TestWriteCommonFields_Empty(t *testing.T) {
	var buf bytes.Buffer
	WriteCommonFields(&buf, Info{})
	if buf.Len() > 0 {
		t.Fatalf("expected empty output for empty Info, got %q", buf.String())
	}
}

func TestWriteNotProvisionedJSON(t *testing.T) {
	var buf bytes.Buffer
	WriteNotProvisionedJSON(&buf)
	var result map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &result); err != nil {
		t.Fatalf("unexpected error unmarshaling JSON: %v", err)
	}
	if result["provisioned"] != false {
		t.Fatalf("expected provisioned=false, got %v", result["provisioned"])
	}
	if result["status"] != "not_provisioned" {
		t.Fatalf("expected status=not_provisioned, got %v", result["status"])
	}
}

func TestWriteNotProvisionedText(t *testing.T) {
	var buf bytes.Buffer
	WriteNotProvisionedText(&buf, "Perforce", "fabrica perforce create")
	out := buf.String()
	if !strings.Contains(out, "Perforce is not provisioned") {
		t.Errorf("missing module name: %q", out)
	}
	if !strings.Contains(out, "fabrica perforce create") {
		t.Errorf("missing create command: %q", out)
	}
}

func TestWriteSecurityGroup(t *testing.T) {
	t.Run("with sg id", func(t *testing.T) {
		var buf bytes.Buffer
		WriteSecurityGroup(&buf, "sg-abc123")
		if !strings.Contains(buf.String(), "sg-abc123") {
			t.Errorf("missing sg id: %q", buf.String())
		}
	})
	t.Run("empty sg id", func(t *testing.T) {
		var buf bytes.Buffer
		WriteSecurityGroup(&buf, "")
		if buf.Len() > 0 {
			t.Errorf("expected empty output: %q", buf.String())
		}
	})
}

func TestWriteProbeStatusText(t *testing.T) {
	t.Run("reachable with version", func(t *testing.T) {
		var buf bytes.Buffer
		info := Info{ProbeAttempted: true, Reachable: true}
		WriteProbeStatusText(&buf, info, "Helix Core", "2024.2")
		out := buf.String()
		if !strings.Contains(out, "responding") || !strings.Contains(out, "2024.2") {
			t.Errorf("unexpected output: %q", out)
		}
	})
	t.Run("reachable without version", func(t *testing.T) {
		var buf bytes.Buffer
		info := Info{ProbeAttempted: true, Reachable: true}
		WriteProbeStatusText(&buf, info, "Horde", "")
		if !strings.Contains(buf.String(), "Horde:    responding") {
			t.Errorf("unexpected output: %q", buf.String())
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		var buf bytes.Buffer
		info := Info{ProbeAttempted: true, Reachable: false}
		WriteProbeStatusText(&buf, info, "Lore", "")
		if !strings.Contains(buf.String(), "unreachable from this machine") {
			t.Errorf("unexpected output: %q", buf.String())
		}
	})
	t.Run("provisioning", func(t *testing.T) {
		var buf bytes.Buffer
		info := Info{ModuleStatus: "provisioning"}
		WriteProbeStatusText(&buf, info, "DDC", "")
		if !strings.Contains(buf.String(), "setting up") {
			t.Errorf("unexpected output: %q", buf.String())
		}
	})
	t.Run("no probe no provisioning", func(t *testing.T) {
		var buf bytes.Buffer
		info := Info{ModuleStatus: "ready"}
		WriteProbeStatusText(&buf, info, "DDC", "")
		if buf.Len() > 0 {
			t.Errorf("expected empty output: %q", buf.String())
		}
	})
}

func TestProbeStatus(t *testing.T) {
	t.Run("responding", func(t *testing.T) {
		info := Info{ProbeAttempted: true, Reachable: true}
		if got := ProbeStatus(info); got != "responding" {
			t.Errorf("ProbeStatus = %q, want responding", got)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		info := Info{ProbeAttempted: true, Reachable: false}
		if got := ProbeStatus(info); got != "unreachable" {
			t.Errorf("ProbeStatus = %q, want unreachable", got)
		}
	})
	t.Run("setting up", func(t *testing.T) {
		info := Info{ModuleStatus: "provisioning"}
		if got := ProbeStatus(info); got != "setting up" {
			t.Errorf("ProbeStatus = %q, want setting up", got)
		}
	})
	t.Run("no probe info", func(t *testing.T) {
		info := Info{ModuleStatus: "ready"}
		if got := ProbeStatus(info); got != "" {
			t.Errorf("ProbeStatus = %q, want empty", got)
		}
	})
}

func TestNewBaseStatusOutput(t *testing.T) {
	info := Info{
		ModuleStatus: "ready",
		InstanceID:   "i-abc",
		SGID:         "sg-xyz",
		InstanceType: "m5.xlarge",
		PrivateIP:    "10.0.1.1",
	}
	b := NewBaseStatusOutput(info)
	if b.Provisioned != true {
		t.Error("Provisioned should be true")
	}
	if b.Status != "ready" {
		t.Errorf("Status = %q, want ready", b.Status)
	}
	if b.InstanceID != "i-abc" {
		t.Errorf("InstanceID = %q, want i-abc", b.InstanceID)
	}
	if b.SGID != "sg-xyz" {
		t.Errorf("SGID = %q, want sg-xyz", b.SGID)
	}
	if b.InstanceType != "m5.xlarge" {
		t.Errorf("InstanceType = %q, want m5.xlarge", b.InstanceType)
	}
	if b.PrivateIP != "10.0.1.1" {
		t.Errorf("PrivateIP = %q, want 10.0.1.1", b.PrivateIP)
	}
}

func TestBaseStatusOutputFillFromInfo(t *testing.T) {
	b := BaseStatusOutput{}
	info := Info{
		ModuleStatus: "provisioning",
		InstanceID:   "i-123",
		PrivateIP:    "10.0.2.2",
	}
	b.FillFromInfo(info)
	if b.Provisioned != true {
		t.Error("Provisioned should be true")
	}
	if b.Status != "provisioning" {
		t.Errorf("Status = %q, want provisioning", b.Status)
	}
	if b.InstanceID != "i-123" {
		t.Errorf("InstanceID = %q, want i-123", b.InstanceID)
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	WriteJSON(&buf, map[string]any{"key": "value", "nested": map[string]any{"a": 1}})
	out := buf.String()
	if !strings.Contains(out, `"key": "value"`) {
		t.Errorf("missing key: %q", out)
	}
	// Verify it's indented
	if !strings.Contains(out, "  ") {
		t.Error("expected indented JSON")
	}
}
