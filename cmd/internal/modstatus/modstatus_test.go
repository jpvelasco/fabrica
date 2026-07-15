package modstatus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
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
	assertContains(t, err.Error(), "querying instance")
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
	assertContains(t, err.Error(), "reading state")
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
	assertContains(t, out.String(), "Warning")
	assertContains(t, out.String(), "disk full")
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
	assertContains(t, out.String(), "Timed out")
	assertContains(t, out.String(), "Perforce") // DisplayName in the timeout message
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
	assertContains(t, err.Error(), "querying instance")
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

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if len(substr) == 0 {
		return
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q\ndoes not contain\n%q", s, substr)
}
