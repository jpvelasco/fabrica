package setup

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func baseRuntime() globals.Runtime {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Deploy.BuildBucket = "bkt"
	return globals.Runtime{Config: cfg, Provider: nil}
}

func newTestCmd(rt globals.Runtime, out *bytes.Buffer) *command {
	st := fabricastate.NewState("123456789012", "us-east-1")
	created := map[string]int{}
	return &command{
		runtime:    rt,
		out:        out,
		costs:      fabricacost.Global,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(s *fabricastate.State) error { st = s; return nil },
		createResource: func(_ context.Context, r *cloud.Resource) error {
			created[r.TypeName]++
			r.Identifier = r.TypeName + "-id"
			return nil
		},
		getResource: func(_ context.Context, _ *cloud.Resource) error { return nil },
		confirm:     func(string) bool { return true },
	}
}

func TestSetupCreatesRoleAndAlias(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(baseRuntime(), &out)
	c.assumeYes = true
	// Provide identity via a fake provider on the runtime.
	c.runtime.Provider = fakeProvider{}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "IAM role") || !strings.Contains(s, "alias") {
		t.Errorf("expected role+alias creation output:\n%s", s)
	}
}

func TestSetupRequiresBuildBucket(t *testing.T) {
	var out bytes.Buffer
	rt := baseRuntime()
	rt.Config.Deploy.BuildBucket = ""
	c := newTestCmd(rt, &out)
	c.assumeYes = true
	c.runtime.Provider = fakeProvider{}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when buildBucket is unset")
	}
}

func TestSetupDryRunNoWrites(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(baseRuntime(), &out)
	c.dryRun = true
	c.runtime.Provider = fakeProvider{}
	writes := 0
	c.createResource = func(context.Context, *cloud.Resource) error { writes++; return nil }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if writes != 0 {
		t.Errorf("dry-run created %d resources", writes)
	}
	if !strings.Contains(out.String(), "Cost estimate") {
		t.Errorf("dry-run should show cost:\n%s", out.String())
	}
}

func TestSetupConfirmRejected(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(baseRuntime(), &out)
	c.runtime.Provider = fakeProvider{}
	c.confirm = func(string) bool { return false }
	writes := 0
	c.createResource = func(context.Context, *cloud.Resource) error { writes++; return nil }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if writes != 0 {
		t.Errorf("rejected confirm still created %d resources", writes)
	}
}

// fakeProvider supplies Identity for the command.
type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (fakeProvider) Resources() cloud.ResourceClient { return nil }
