package approve

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
)

const approveMarker = "MODE='approve'"

// fakeEnrollment stands in for the coordinator. It answers the list call with
// listJSON and the approval call with an approved summary built from the hosts
// the script was given, and records every call it receives.
type fakeEnrollment struct {
	pending  []string
	unnamed  int
	calls    []string
	failWith *cloud.RemoteResult
	err      error
}

func (f *fakeEnrollment) run(ctx context.Context, _ string, commands []string) (cloud.RemoteResult, error) {
	if err := ctx.Err(); err != nil {
		return cloud.RemoteResult{}, err
	}
	script := commands[0]
	f.calls = append(f.calls, script)
	if f.failWith != nil {
		return *f.failWith, nil
	}
	if f.err != nil {
		return cloud.RemoteResult{}, f.err
	}
	if !strings.Contains(script, approveMarker) {
		return cloud.RemoteResult{Stdout: summaryJSON(f.pending, nil, f.unnamed)}, nil
	}
	return cloud.RemoteResult{Stdout: summaryJSON(f.pending, requestedHosts(script), f.unnamed)}, nil
}

func summaryJSON(pending, approved []string, unnamed int) string {
	if pending == nil {
		pending = []string{}
	}
	if approved == nil {
		approved = []string{}
	}
	data, _ := json.Marshal(map[string]any{"pending": pending, "approved": approved, "unnamed": unnamed})
	return string(data)
}

// requestedHosts recovers the hosts the rendered approval script was asked to
// approve by reading them back from its base64 header.
func requestedHosts(script string) []string {
	for _, line := range strings.Split(script, "\n") {
		if v, ok := strings.CutPrefix(line, "HOSTS_B64="); ok {
			raw := strings.Trim(v, "'")
			var hosts []string
			if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
				_ = json.Unmarshal(decoded, &hosts)
			}
			return hosts
		}
	}
	return nil
}

func (f *fakeEnrollment) approveCalls() int {
	n := 0
	for _, s := range f.calls {
		if strings.Contains(s, approveMarker) {
			n++
		}
	}
	return n
}

func coordinatorState() *state.State {
	st := state.NewState("", "")
	st.UpsertModule("horde", "ami-test", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coordinator"},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-coordinator"},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-agents", Properties: map[string]string{"role": "agent"}},
	})
	return st
}

func newTestCommand(t *testing.T, fake *fakeEnrollment) (command, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &out,
		readState: func(context.Context) (*state.State, error) { return coordinatorState(), nil },
		runRemote: fake.run,
		confirm:   func(string) bool { t.Fatal("confirm must not be called"); return false },
		assumeYes: true,
	}, &out
}

func mustRun(t *testing.T, c command) {
	t.Helper()
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestListPrintsPendingWithoutApproving(t *testing.T) {
	fake := &fakeEnrollment{pending: []string{"agent-1", "agent-2"}}
	c, out := newTestCommand(t, fake)
	mustRun(t, c)
	if fake.approveCalls() != 0 || len(fake.calls) != 1 {
		t.Fatalf("list must make exactly one call and no approval: %d calls", len(fake.calls))
	}
	for _, want := range []string{"Pending agent enrollments:", "  agent-1", "  agent-2"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestListWithNoPendingIsSuccess(t *testing.T) {
	fake := &fakeEnrollment{}
	c, out := newTestCommand(t, fake)
	mustRun(t, c)
	if !strings.Contains(out.String(), "No pending agent enrollments.") {
		t.Fatalf("output = %q", out)
	}
}

func TestDryRunListsAndNeverApproves(t *testing.T) {
	fake := &fakeEnrollment{pending: []string{"agent-1"}}
	c, out := newTestCommand(t, fake)
	c.dryRun = true
	c.hosts = []string{"agent-1"}
	mustRun(t, c)
	if len(fake.calls) != 1 || fake.approveCalls() != 0 {
		t.Fatalf("dry run must make only the list call; calls=%d", len(fake.calls))
	}
	if !strings.Contains(out.String(), "would approve agent-1. No approval was sent.") {
		t.Fatalf("output = %q", out)
	}
}

func TestApproveHostsAfterConfirmation(t *testing.T) {
	fake := &fakeEnrollment{pending: []string{"agent-1", "agent-2"}}
	c, out := newTestCommand(t, fake)
	c.assumeYes = false
	var asked string
	c.confirm = func(msg string) bool { asked = msg; return true }
	c.hosts = []string{"agent-2"}
	mustRun(t, c)
	if fake.approveCalls() != 1 || len(fake.calls) != 2 {
		t.Fatalf("expected list + one approval, got %d calls", len(fake.calls))
	}
	if !strings.Contains(asked, "agent-2") || strings.Contains(asked, "agent-1") {
		t.Fatalf("confirmation named the wrong hosts: %q", asked)
	}
	if !strings.Contains(out.String(), "Approved 1 agent enrollment(s): agent-2") {
		t.Fatalf("output = %q", out)
	}
}

func TestDeclinedConfirmationApprovesNothing(t *testing.T) {
	fake := &fakeEnrollment{pending: []string{"agent-1"}}
	c, out := newTestCommand(t, fake)
	c.assumeYes = false
	c.confirm = func(string) bool { return false }
	c.hosts = []string{"agent-1"}
	mustRun(t, c)
	if fake.approveCalls() != 0 {
		t.Fatal("declined confirmation must not approve")
	}
	if !strings.Contains(out.String(), "Approval cancelled") {
		t.Fatalf("output = %q", out)
	}
}

func TestSelectionFailsBeforeAnyApproval(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pending []string
		unnamed int
		hosts   []string
		all     bool
	}{
		{"unknown host", []string{"agent-1"}, 0, []string{"ghost"}, false},
		{"one of several unknown", []string{"agent-1"}, 0, []string{"agent-1", "ghost"}, false},
		{"duplicate pending", []string{"agent-1", "agent-1"}, 0, []string{"agent-1"}, false},
		{"all with unnamed", []string{"agent-1"}, 1, nil, true},
		{"all with duplicate", []string{"agent-1", "agent-1"}, 0, nil, true},
		{"invalid host flag", nil, 0, []string{"bad host"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEnrollment{pending: tc.pending, unnamed: tc.unnamed}
			c, _ := newTestCommand(t, fake)
			c.hosts = tc.hosts
			c.all = tc.all
			if err := c.run(context.Background()); err == nil {
				t.Fatal("expected rejection")
			}
			if fake.approveCalls() != 0 {
				t.Fatal("rejected selection must never reach the approval call")
			}
		})
	}
}

// The per-run host cap is checked with the rest of the selection, so --all
// over a large pool fails before the confirmation prompt and on --dry-run.
func TestAllOverHostCapFailsBeforeConfirmation(t *testing.T) {
	pending := make([]string, 101)
	for i := range pending {
		pending[i] = fmt.Sprintf("agent-%d", i)
	}
	for _, dry := range []bool{false, true} {
		fake := &fakeEnrollment{pending: pending}
		c, _ := newTestCommand(t, fake)
		c.all, c.dryRun, c.assumeYes = true, dry, false
		c.confirm = func(string) bool { t.Fatal("confirm must not be reached"); return false }
		err := c.run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "approve them in batches with --host") {
			t.Fatalf("dryRun=%v: err = %v, want the batch hint", dry, err)
		}
		if fake.approveCalls() != 0 {
			t.Fatal("an over-cap selection must never reach the approval call")
		}
	}
}

func TestAllApprovesEveryNamedPending(t *testing.T) {
	fake := &fakeEnrollment{pending: []string{"agent-1", "agent-2"}}
	c, out := newTestCommand(t, fake)
	c.all = true
	mustRun(t, c)
	if fake.approveCalls() != 1 || !strings.Contains(out.String(), "agent-1, agent-2") {
		t.Fatalf("--all did not approve both: calls=%d out=%q", fake.approveCalls(), out)
	}
}

func TestAllWithNothingPendingIsNoop(t *testing.T) {
	fake := &fakeEnrollment{}
	c, out := newTestCommand(t, fake)
	c.all = true
	mustRun(t, c)
	if fake.approveCalls() != 0 || !strings.Contains(out.String(), "No pending agent enrollments.") {
		t.Fatalf("expected no-op; out=%q", out)
	}
}

func TestJSONOutputIsAllowlisted(t *testing.T) {
	fake := &fakeEnrollment{pending: []string{"agent-1"}, unnamed: 2}
	c, out := newTestCommand(t, fake)
	c.jsonOut = true
	c.hosts = []string{"agent-1"}
	c.dryRun = true
	mustRun(t, c)
	var got result
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	if !got.DryRun || len(got.Pending) != 1 || got.Unnamed != 2 || len(got.Selected) != 1 || got.Approved == nil {
		t.Fatalf("json = %+v", got)
	}
	if strings.Contains(out.String(), "Note:") {
		t.Fatal("text notes must not corrupt JSON output")
	}
}

func TestWarnsWhenNoAgentPoolRecorded(t *testing.T) {
	fake := &fakeEnrollment{}
	c, out := newTestCommand(t, fake)
	c.readState = func(context.Context) (*state.State, error) {
		st := state.NewState("", "")
		st.UpsertModule("horde", "ami", "ready", []state.ModuleResource{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coordinator"}})
		return st, nil
	}
	mustRun(t, c)
	if !strings.Contains(out.String(), "no agent pool is recorded") {
		t.Fatalf("expected warning, got %q", out)
	}
}

func TestRunFailuresAreActionable(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name   string
		change func(*command, *fakeEnrollment)
		ctx    context.Context
		want   string
	}{
		{"state read", func(c *command, _ *fakeEnrollment) {
			c.readState = func(context.Context) (*state.State, error) { return nil, errors.New("read failed") }
		}, nil, "read failed"},
		{"no coordinator", func(c *command, _ *fakeEnrollment) {
			c.readState = func(context.Context) (*state.State, error) { return state.NewState("", ""), nil }
		}, nil, "fabrica horde create"},
		{"no instance", func(c *command, _ *fakeEnrollment) {
			c.readState = func(context.Context) (*state.State, error) {
				st := state.NewState("", "")
				st.UpsertModule("horde", "ami", "ready", []state.ModuleResource{{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg"}})
				return st, nil
			}
		}, nil, "instance is not recorded"},
		{"no remote runner", func(c *command, _ *fakeEnrollment) { c.runRemote = nil }, nil, "SSM Run Command"},
		{"non-zero exit", func(_ *command, f *fakeEnrollment) {
			f.failWith = &cloud.RemoteResult{ExitCode: 1, Stdout: "SECRET-KEY-VALUE", Stderr: "hostName=agent-1"}
		}, nil, "SSM agent is online"},
		{"ssm error", func(_ *command, f *fakeEnrollment) { f.err = errors.New("SECRET-KEY-VALUE") }, nil, "SSM agent is online"},
		{"interrupted", func(*command, *fakeEnrollment) {}, cancelled, "interrupted"},
		{"malformed output", func(_ *command, f *fakeEnrollment) {
			f.failWith = &cloud.RemoteResult{Stdout: "not json SECRET-KEY-VALUE"}
		}, nil, "expected summary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEnrollment{pending: []string{"agent-1"}}
			c, _ := newTestCommand(t, fake)
			tc.change(&c, fake)
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			err := c.run(ctx)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "SECRET-KEY-VALUE") {
				t.Fatalf("error leaked coordinator output: %v", err)
			}
		})
	}
}

func TestInvalidHostFlagIsRejectedBeforeAnyCall(t *testing.T) {
	fake := &fakeEnrollment{pending: []string{"agent-1"}}
	c, _ := newTestCommand(t, fake)
	c.hosts = []string{"agent-1", "agent-1"}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("duplicate host must be rejected")
	}
	if len(fake.calls) != 0 {
		t.Fatal("validation must precede the SSM list call")
	}
}
