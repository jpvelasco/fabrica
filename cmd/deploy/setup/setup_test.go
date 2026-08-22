package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
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
	c.runtime.Provider = &testutil.TestProvider{}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "IAM role") || !strings.Contains(s, "alias") {
		t.Errorf("expected role+alias creation output:\n%s", s)
	}
}

func TestSetupRoleCreateErrorPropagates(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(baseRuntime(), &out)
	c.assumeYes = true
	c.runtime.Provider = &testutil.TestProvider{}
	c.createResource = func(context.Context, *cloud.Resource) error {
		return errors.New("AccessDenied")
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected IAM role creation error")
	}
	if !strings.Contains(err.Error(), "IAM role: AccessDenied") {
		t.Fatalf("error = %q, want IAM role creation context", err)
	}
}

func TestSetupRequiresBuildBucket(t *testing.T) {
	var out bytes.Buffer
	rt := baseRuntime()
	rt.Config.Deploy.BuildBucket = ""
	c := newTestCmd(rt, &out)
	c.assumeYes = true
	c.runtime.Provider = &testutil.TestProvider{}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when buildBucket is unset")
	}
}

func TestSetupDryRunNoWrites(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(baseRuntime(), &out)
	c.dryRun = true
	c.runtime.Provider = &testutil.TestProvider{}
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
	c.runtime.Provider = &testutil.TestProvider{}
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

func TestSetupIdempotentExistingResources(t *testing.T) {
	var out bytes.Buffer
	// Pre-seed state with existing role + alias so existingResource returns true.
	st := &fabricastate.State{
		Account: "123456789012",
		Region:  "us-east-1",
		Modules: []fabricastate.ModuleState{{
			Name:    "deploy",
			Version: "fabrica-deploy",
			Status:  "ready",
			Resources: []fabricastate.ModuleResource{
				{TypeName: "AWS::IAM::Role", Identifier: "existing-role"},
				{TypeName: "AWS::GameLift::Alias", Identifier: "existing-alias"},
			},
		}},
	}
	created := map[string]int{}
	c := &command{
		runtime:    baseRuntime(),
		assumeYes:  true,
		out:        &out,
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
	c.runtime.Provider = &testutil.TestProvider{}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "already exists") {
		t.Errorf("expected skip message for existing resources:\n%s", s)
	}
	if created["AWS::IAM::Role"] > 0 {
		t.Error("should not create IAM role when it already exists")
	}
	if created["AWS::GameLift::Alias"] > 0 {
		t.Error("should not create alias when it already exists")
	}
}

func TestSetupNoCreateResource(t *testing.T) {
	var out bytes.Buffer
	c := &command{
		runtime:    baseRuntime(),
		assumeYes:  true,
		out:        &out,
		costs:      fabricacost.Global,
		readState:  func() (*fabricastate.State, error) { return fabricastate.NewState("123456789012", "us-east-1"), nil },
		writeState: func(s *fabricastate.State) error { return nil },
		confirm:    func(string) bool { return true },
	}
	c.runtime.Provider = &testutil.TestProvider{}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when createResource is nil")
	}
}

// TestSetupPreservesFleetAndBuildRecords verifies that an idempotent re-setup
// does not drop build/fleet records written by later promote runs.
func TestSetupPreservesFleetAndBuildRecords(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("deploy", "fabrica-deploy", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "existing-role"},
		{TypeName: "AWS::GameLift::Alias", Identifier: "existing-alias"},
		{TypeName: "AWS::GameLift::Build", Identifier: "build-1", Properties: map[string]string{"buildVersion": "v1"}},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-1", Properties: map[string]string{"role": "active"}},
	})
	c := &command{
		runtime:    baseRuntime(),
		assumeYes:  true,
		out:        &out,
		costs:      fabricacost.Global,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(s *fabricastate.State) error { st = s; return nil },
		createResource: func(_ context.Context, r *cloud.Resource) error {
			r.Identifier = r.TypeName + "-id"
			return nil
		},
		getResource: func(_ context.Context, _ *cloud.Resource) error { return nil },
		confirm:     func(string) bool { return true },
	}
	c.runtime.Provider = &testutil.TestProvider{}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	m := st.GetModule("deploy")
	var haveRole, haveAlias, haveBuild, haveFleet bool
	for _, r := range m.Resources {
		switch {
		case r.TypeName == "AWS::IAM::Role":
			haveRole = true
		case r.TypeName == "AWS::GameLift::Alias":
			haveAlias = true
		case r.TypeName == "AWS::GameLift::Build" && r.Identifier == "build-1":
			haveBuild = true
		case r.TypeName == "AWS::GameLift::Fleet" && r.Identifier == "fleet-1" && r.Properties["role"] == "active":
			haveFleet = true
		}
	}
	if !haveRole || !haveAlias || !haveBuild || !haveFleet {
		t.Fatalf("re-setup dropped records (role=%v alias=%v build=%v fleet=%v): %+v",
			haveRole, haveAlias, haveBuild, haveFleet, m.Resources)
	}
}
