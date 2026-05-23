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

type fakeHordeClient struct {
	submitErr   error
	submitJobID string
	statusMap   map[string]string
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
	if err := os.WriteFile(badXML, []byte("not xml <<<"), 0644); err != nil {
		t.Fatalf("writing bad XML: %v", err)
	}

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

// TestSubmitWaitPollsUntilComplete verifies --wait polls until job is complete.
func TestSubmitWaitPollsUntilComplete(t *testing.T) {
	var out bytes.Buffer
	st := hordeProvisionedState()
	callCount := 0
	seqClient := &sequentialFakeClient{
		submitJobID: "job-wait-001",
		statusSeq:   []string{"waiting", "running", "complete"},
		idx:         &callCount,
	}
	c := newTestCommand(&out, seqClient, st)
	c.buildGraphPath = writeTempBuildGraph(t)
	c.wait = true

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

// TestSubmitClientError verifies connection error is returned.
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
	assertContains(t, err.Error(), "connection refused")
}

// TestSubmitReadStateError verifies error is surfaced when readState fails.
func TestSubmitReadStateError(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, &fakeHordeClient{}, nil)
	c.readState = func() (*fabricastate.State, error) {
		return nil, errors.New("disk failure")
	}
	c.buildGraphPath = writeTempBuildGraph(t)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
	assertContains(t, err.Error(), "reading state")
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
