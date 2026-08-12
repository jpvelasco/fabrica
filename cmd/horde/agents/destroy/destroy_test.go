package destroy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestResolveAccount_FromConfig(t *testing.T) {
	st := fabricastate.NewState("state-account", "us-east-1")
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "config-account"
	c := command{
		runtime: globals.Runtime{Config: cfg},
	}

	got := c.resolveAccount(st)
	if got != "config-account" {
		t.Errorf("resolveAccount = %q, want config-account", got)
	}
}

func TestResolveAccount_FromState(t *testing.T) {
	st := fabricastate.NewState("state-account", "us-east-1")
	c := command{
		runtime: globals.Runtime{Config: nil},
	}

	got := c.resolveAccount(st)
	if got != "state-account" {
		t.Errorf("resolveAccount = %q, want state-account", got)
	}
}

func TestNewTeardown_ReturnsNoOp(t *testing.T) {
	cmd := NewTeardown(globals.Runtime{}, io.Discard)
	// NewTeardown returns an empty teardown.Command (no-op) because
	// the orchestrator uses hordedestroy.NewTeardown which covers both
	// coordinator and agent resources.
	if cmd.DeleteHook != nil {
		t.Error("expected NewTeardown to return a no-op command with nil DeleteHook")
	}
	if cmd.DeleteResource != nil {
		t.Error("expected NewTeardown to return a no-op command with nil DeleteResource")
	}
}

func TestIsAgentResource_AgentRole(t *testing.T) {
	r := fabricastate.ModuleResource{
		TypeName:   "AWS::EC2::SecurityGroup",
		Identifier: "sg-agent123",
		Properties: map[string]string{"role": "agent"},
	}
	if !isAgentResource(r) {
		t.Error("expected isAgentResource = true for agent role")
	}
}

func TestIsAgentResource_NonAgent(t *testing.T) {
	r := fabricastate.ModuleResource{
		TypeName:   "AWS::EC2::Instance",
		Identifier: "i-coordinator",
	}
	if isAgentResource(r) {
		t.Error("expected isAgentResource = false for non-agent")
	}
}

func TestIsAgentResource_DifferentRole(t *testing.T) {
	r := fabricastate.ModuleResource{
		TypeName:   "AWS::EC2::SecurityGroup",
		Identifier: "sg-coord",
		Properties: map[string]string{"role": "coordinator"},
	}
	if isAgentResource(r) {
		t.Error("expected isAgentResource = false for coordinator role")
	}
}

func TestAgentsToDelete_Order(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::IAM::Role", Identifier: "role-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "profile-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::EC2::LaunchTemplate", Identifier: "lt-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-coord", Properties: map[string]string{"role": "coordinator"}},
		},
	}

	resources := agentsToDelete(m)
	if len(resources) != 5 {
		t.Fatalf("want 5 agent resources, got %d", len(resources))
	}

	// Verify deletion order: ASG → LT → profile → role → SG
	wantOrder := []string{
		"AWS::AutoScaling::AutoScalingGroup",
		"AWS::EC2::LaunchTemplate",
		"AWS::IAM::InstanceProfile",
		"AWS::IAM::Role",
		"AWS::EC2::SecurityGroup",
	}
	for i, want := range wantOrder {
		if resources[i].TypeName != want {
			t.Errorf("[%d] TypeName = %q, want %q", i, resources[i].TypeName, want)
		}
	}
}

func TestAgentsToDelete_Empty(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
		},
	}

	resources := agentsToDelete(m)
	if len(resources) != 0 {
		t.Errorf("want 0 resources, got %d", len(resources))
	}
}

func TestAgentsProvisioned_True(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
		},
	}
	if !agentsProvisioned(m) {
		t.Error("expected agentsProvisioned = true")
	}
}

func TestAgentsProvisioned_False(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
		},
	}
	if agentsProvisioned(m) {
		t.Error("expected agentsProvisioned = false")
	}
}

func TestAgentsProvisioned_Empty(t *testing.T) {
	m := &fabricastate.ModuleState{}
	if agentsProvisioned(m) {
		t.Error("expected agentsProvisioned = false for empty module")
	}
}

func TestRemoveResource(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-agent", Properties: map[string]string{"role": "agent"}},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
			{TypeName: "AWS::IAM::Role", Identifier: "role-agent", Properties: map[string]string{"role": "agent"}},
		},
	}

	removeResource(m, "AWS::EC2::SecurityGroup", "sg-agent")
	if len(m.Resources) != 2 {
		t.Fatalf("want 2 resources after removal, got %d", len(m.Resources))
	}
	for _, r := range m.Resources {
		if r.Identifier == "sg-agent" {
			t.Error("sg-agent should have been removed")
		}
	}
}

func TestRemoveResource_NotFound(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
		},
	}

	removeResource(m, "AWS::EC2::SecurityGroup", "sg-nonexistent")
	if len(m.Resources) != 1 {
		t.Errorf("want 1 resource (unchanged), got %d", len(m.Resources))
	}
}

func TestDeleteOneResource_Success(t *testing.T) {
	var deleted []cloud.Resource
	c := command{
		out: &bytes.Buffer{},
		deleteResource: func(ctx context.Context, r *cloud.Resource) error {
			deleted = append(deleted, *r)
			return nil
		},
		writeState: func(st *fabricastate.State) error { return nil },
	}

	st := fabricastate.NewState("123456789012", "us-east-1")
	m := &fabricastate.ModuleState{
		Version: "v1",
		Status:  "ready",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
		},
	}
	st.UpsertModule("horde", m.Version, m.Status, m.Resources)

	res := cloud.Resource{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent"}
	err := c.deleteOneResource(context.Background(), st, m, res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deleted) != 1 {
		t.Fatalf("want 1 delete call, got %d", len(deleted))
	}
	if len(m.Resources) != 0 {
		t.Errorf("want 0 resources after delete, got %d", len(m.Resources))
	}
}

func TestDeleteOneResource_AlreadyDeleted(t *testing.T) {
	var out bytes.Buffer
	c := command{
		out: &out,
		deleteResource: func(ctx context.Context, r *cloud.Resource) error {
			return cloud.ErrResourceNotFound
		},
		writeState: func(st *fabricastate.State) error { return nil },
	}

	st := fabricastate.NewState("123456789012", "us-east-1")
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
		},
	}
	st.UpsertModule("horde", "v1", "ready", m.Resources)

	res := cloud.Resource{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent"}
	err := c.deleteOneResource(context.Background(), st, m, res)
	if err != nil {
		t.Fatalf("unexpected error for already-deleted: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("Already deleted")) {
		t.Error("expected 'Already deleted' in output")
	}
}

func TestDeleteOneResource_DeleteError(t *testing.T) {
	c := command{
		out: &bytes.Buffer{},
		deleteResource: func(ctx context.Context, r *cloud.Resource) error {
			return cloud.ErrResourceNotFound
		},
		writeState: func(st *fabricastate.State) error { return nil },
	}

	st := fabricastate.NewState("123456789012", "us-east-1")
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
		},
	}
	st.UpsertModule("horde", "v1", "ready", m.Resources)

	res := cloud.Resource{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent"}
	err := c.deleteOneResource(context.Background(), st, m, res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Already deleted path should not return an error.
}

func TestApply_NoProvider(t *testing.T) {
	c := command{
		out:            &bytes.Buffer{},
		deleteResource: nil,
	}

	st := fabricastate.NewState("123456789012", "us-east-1")
	m := &fabricastate.ModuleState{}
	resources := []cloud.Resource{{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent"}}

	err := c.apply(context.Background(), st, m, resources)
	if err == nil {
		t.Fatal("expected error when no provider")
	}
}

func TestPrintNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	c := command{out: &out}
	c.printNotProvisioned()
	if !bytes.Contains(out.Bytes(), []byte("not provisioned")) {
		t.Errorf("expected 'not provisioned' in output: %s", out.String())
	}
}

func TestPrintDryRun(t *testing.T) {
	var out bytes.Buffer
	c := command{out: &out}
	m := &fabricastate.ModuleState{Status: "ready"}
	resources := []cloud.Resource{
		{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent"},
		{TypeName: "AWS::EC2::LaunchTemplate", Identifier: "lt-agent"},
	}
	c.printDryRun(m, resources)
	if !bytes.Contains(out.Bytes(), []byte("dry run")) {
		t.Errorf("expected 'dry run' in output: %s", out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("asg-agent")) {
		t.Errorf("expected 'asg-agent' in output: %s", out.String())
	}
}

func TestPrintPlan(t *testing.T) {
	var out bytes.Buffer
	c := command{out: &out}
	m := &fabricastate.ModuleState{Status: "ready"}
	resources := []cloud.Resource{
		{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent"},
	}
	c.printPlan(m, resources)
	if !bytes.Contains(out.Bytes(), []byte("IRREVERSIBLE")) {
		t.Errorf("expected 'IRREVERSIBLE' in output: %s", out.String())
	}
}

func TestRun_ReadStateError(t *testing.T) {
	c := command{
		out:       &bytes.Buffer{},
		readState: func() (*fabricastate.State, error) { return nil, fmt.Errorf("state file missing") },
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
}

func TestRun_NotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := command{
		out:       &out,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	err := c.run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("not provisioned")) {
		t.Errorf("expected 'not provisioned' in output: %s", out.String())
	}
}

func TestRun_DryRun(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
	})
	c := command{
		out:       &out,
		dryRun:    true,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	err := c.run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("dry run")) {
		t.Errorf("expected 'dry run' in output: %s", out.String())
	}
}

func TestRun_AssumeYes(t *testing.T) {
	var out bytes.Buffer
	var deleted []cloud.Resource
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
	})
	c := command{
		out:        &out,
		assumeYes:  true,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(st *fabricastate.State) error { return nil },
		deleteResource: func(ctx context.Context, r *cloud.Resource) error {
			deleted = append(deleted, *r)
			return nil
		},
	}
	err := c.run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("Proceeding without interactive confirmation")) {
		t.Errorf("expected '--yes' message in output: %s", out.String())
	}
	if len(deleted) != 1 {
		t.Errorf("want 1 delete call, got %d", len(deleted))
	}
}

func TestRun_ConfirmationRejected(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
	})
	c := command{
		out:       &out,
		readState: func() (*fabricastate.State, error) { return st, nil },
		confirm:   func(prompt, phrase string) bool { return false },
	}
	err := c.run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("Cancelled")) {
		t.Errorf("expected 'Cancelled' in output: %s", out.String())
	}
}

func TestRun_ConfirmationAccepted(t *testing.T) {
	var out bytes.Buffer
	var deleted []cloud.Resource
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
	})
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	c := command{
		runtime:    globals.Runtime{Config: cfg},
		out:        &out,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(st *fabricastate.State) error { return nil },
		confirm:    func(prompt, phrase string) bool { return true },
		deleteResource: func(ctx context.Context, r *cloud.Resource) error {
			deleted = append(deleted, *r)
			return nil
		},
	}
	err := c.run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("Confirmation accepted")) {
		t.Errorf("expected 'Confirmation accepted' in output: %s", out.String())
	}
	if len(deleted) != 1 {
		t.Errorf("want 1 delete call, got %d", len(deleted))
	}
}
