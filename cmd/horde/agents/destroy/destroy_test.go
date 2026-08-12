package destroy

import (
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
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
