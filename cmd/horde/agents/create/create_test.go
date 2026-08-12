package create

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestAgentsProvisioned_True(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
		{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
	})
	if !agentsProvisioned(st) {
		t.Error("expected agentsProvisioned = true")
	}
}

func TestAgentsProvisioned_False_NoModule(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	if agentsProvisioned(st) {
		t.Error("expected agentsProvisioned = false when no module")
	}
}

func TestAgentsProvisioned_False_NoASG(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	if agentsProvisioned(st) {
		t.Error("expected agentsProvisioned = false when no ASG")
	}
}

func TestResolveCoordinator_MissingModule(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := command{
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	_, _, _, err := c.resolveCoordinator(context.Background())
	if err == nil {
		t.Fatal("expected error when horde module not found")
	}
}

func TestResolveCoordinator_MissingInstance(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coord"},
	})
	c := command{
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	_, _, _, err := c.resolveCoordinator(context.Background())
	if err == nil {
		t.Fatal("expected error when coordinator instance not found")
	}
}

func TestResolveCoordinator_NoProvider(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	c := command{
		readState:   func() (*fabricastate.State, error) { return st, nil },
		getResource: nil,
	}
	_, _, _, err := c.resolveCoordinator(context.Background())
	if err == nil {
		t.Fatal("expected error when no provider")
	}
}

func TestResolveCoordinator_DefaultPort(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	cfg := config.Defaults()
	c := command{
		runtime:   globals.Runtime{Config: cfg},
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"PrivateIpAddress":"10.0.1.50"}`)
			return nil
		},
	}
	_, port, _, err := c.resolveCoordinator(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Config.Horde.Port defaults to 0 → should fall back to 5000.
	if port != 5000 {
		t.Errorf("port = %d, want 5000 (default)", port)
	}
}

func TestResolveCoordinator_CustomPort(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	cfg := config.Defaults()
	cfg.Horde.Port = 8080
	c := command{
		runtime:   globals.Runtime{Config: cfg},
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"PrivateIpAddress":"10.0.1.50"}`)
			return nil
		},
	}
	_, port, _, err := c.resolveCoordinator(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != 8080 {
		t.Errorf("port = %d, want 8080 (custom)", port)
	}
}
