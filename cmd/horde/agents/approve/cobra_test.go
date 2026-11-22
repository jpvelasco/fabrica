package approve_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/agents/approve"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
)

const listSummary = `{"pending":["agent-1","agent-2"],"approved":[],"unnamed":0}`

// runApprove wires the real command against a RemoteCommandProvider whose
// queued results answer the list call and then the approval call.
func runApprove(t *testing.T, queued []cloud.RemoteResult, args ...string) (string, *testutil.RemoteCommandProvider, error) {
	t.Helper()
	t.Chdir(t.TempDir())
	st := state.NewState("", "")
	st.UpsertModule("horde", "ami", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coordinator"},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-coordinator"},
	})
	if err := state.WriteStateTo(st, ".fabrica/state.json"); err != nil {
		t.Fatal(err)
	}
	provider := &testutil.RemoteCommandProvider{Queued: queued}
	var out bytes.Buffer
	root, options := testutil.BuildTestSubcommand(&out)
	cfg := config.Defaults()
	root.AddCommand(approve.New(func() (globals.Runtime, error) {
		return globals.Runtime{Config: cfg, Provider: provider}, nil
	}, options, &out))
	root.SetArgs(append([]string{"approve"}, args...))
	err := root.Execute()
	return out.String(), provider, err
}

func TestApproveCobraListsByDefault(t *testing.T) {
	out, provider, err := runApprove(t, []cloud.RemoteResult{{Stdout: listSummary}})
	if err != nil {
		t.Fatal(err)
	}
	if provider.RunCommandCalls != 1 || provider.LastRunCommandInstanceID != "i-coordinator" {
		t.Fatalf("calls=%d instance=%q", provider.RunCommandCalls, provider.LastRunCommandInstanceID)
	}
	if !strings.Contains(out, "agent-1") || !strings.Contains(out, "agent-2") {
		t.Fatalf("output = %q", out)
	}
}

func TestApproveCobraApprovesHostWithYes(t *testing.T) {
	approved := `{"pending":["agent-1","agent-2"],"approved":["agent-2"],"unnamed":0}`
	out, provider, err := runApprove(t, []cloud.RemoteResult{{Stdout: listSummary}, {Stdout: approved}}, "--host", "agent-2", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if provider.RunCommandCalls != 2 {
		t.Fatalf("expected list then approval, got %d calls", provider.RunCommandCalls)
	}
	if !strings.Contains(out, "Approved 1 agent enrollment(s): agent-2") {
		t.Fatalf("output = %q", out)
	}
}

func TestApproveCobraDryRunMakesOnlyTheListCall(t *testing.T) {
	_, provider, err := runApprove(t, []cloud.RemoteResult{{Stdout: listSummary}}, "--host", "agent-1", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if provider.RunCommandCalls != 1 {
		t.Fatalf("dry run made %d SSM calls", provider.RunCommandCalls)
	}
}

func TestApproveCobraHostAndAllAreMutuallyExclusive(t *testing.T) {
	_, provider, err := runApprove(t, []cloud.RemoteResult{{Stdout: listSummary}}, "--host", "agent-1", "--all", "--yes")
	if err == nil || !strings.Contains(err.Error(), "none of the others") {
		t.Fatalf("expected mutual exclusion error, got %v", err)
	}
	if provider.RunCommandCalls != 0 {
		t.Fatal("exclusive flags must fail before any SSM call")
	}
}

func TestApproveCobraJSONOutput(t *testing.T) {
	out, _, err := runApprove(t, []cloud.RemoteResult{{Stdout: listSummary}}, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Pending  []string `json:"pending"`
		Approved []string `json:"approved"`
		Selected []string `json:"selected"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json output is not JSON: %v\n%s", err, out)
	}
	if len(got.Pending) != 2 || got.Approved == nil || got.Selected == nil {
		t.Fatalf("json = %+v", got)
	}
}

func TestApproveCobraRejectsArguments(t *testing.T) {
	if _, _, err := runApprove(t, nil, "agent-1"); err == nil {
		t.Fatal("positional arguments must be rejected")
	}
}
