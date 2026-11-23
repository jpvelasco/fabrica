package create

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/instanceprofile"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// A missing agent profile fails run before network placement and before any create.
func TestRunReferencedProfileMissingFailsBeforePlacement(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coord"},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-coord"},
	})
	cfg := config.Defaults()
	cfg.Horde.Agents.AmiID = "ami-agent123"
	cfg.Horde.Agents.InstanceProfile = "workload"
	provider := &testutil.InstanceProfileProvider{}
	var out bytes.Buffer
	c := command{
		runtime:   globals.Runtime{Config: cfg, Provider: provider},
		out:       &out,
		assumeYes: true,
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(_ context.Context, r *cloud.Resource) error {
			switch r.TypeName {
			case cloud.TypeAWSEC2Instance:
				r.ActualState = json.RawMessage(`{"PrivateIpAddress":"10.0.1.50","SubnetId":"subnet-abc123"}`)
			case cloud.TypeAWSEC2SecurityGroup:
				r.ActualState = json.RawMessage(`{"VpcId":"vpc-abc123"}`)
			}
			return nil
		},
	}
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "horde.agents.instanceProfile") {
		t.Fatalf("run error = %v, want a horde.agents.instanceProfile error", err)
	}
	if len(provider.CreatedTypes) != 0 {
		t.Errorf("created %v before the profile check failed", provider.CreatedTypes)
	}
}

// referencedProfilePlan is the agent pool plan for a pre-created profile.
func referencedProfilePlan() *horde.AgentsCreatePlan {
	plan := agentsPlanForPrint()
	plan.Profile = instanceprofile.Ref{Name: "workload"}
	plan.InstanceProfileName = "workload"
	return plan
}

func TestApplyCreateReferencedProfileMakesNoIAMCreate(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coord"},
	})
	var created []string
	c := command{
		out: &out,
		createResource: func(_ context.Context, r *cloud.Resource) error {
			created = append(created, r.TypeName)
			r.Identifier = "resource-" + r.TypeName
			return nil
		},
		writeState: func(*fabricastate.State) error { return nil },
	}

	if err := c.applyCreate(context.Background(), st, referencedProfilePlan()); err != nil {
		t.Fatalf("applyCreate: %v", err)
	}
	for _, typeName := range created {
		if typeName == cloud.TypeAWSIAMRole || typeName == cloud.TypeAWSIAMInstanceProfile {
			t.Errorf("created %s, want no IAM create calls for a referenced profile (created: %v)", typeName, created)
		}
	}
	if len(created) != 3 {
		t.Errorf("created %v, want the agent SG, launch template, and ASG only", created)
	}

	var profile *fabricastate.ModuleResource
	m := st.GetModule("horde")
	for i := range m.Resources {
		if m.Resources[i].TypeName == cloud.TypeAWSIAMInstanceProfile {
			profile = &m.Resources[i]
		}
	}
	if profile == nil || profile.Identifier != "workload" || profile.Properties["external"] != "true" {
		t.Errorf("profile record = %+v, want an external workload record", profile)
	}
	if !strings.Contains(out.String(), "referenced (pre-created, not managed): workload") {
		t.Errorf("apply output = %q, want the profile reported as pre-created", out.String())
	}
}

func TestPrintDryRunReferencedProfileRow(t *testing.T) {
	var out bytes.Buffer
	c := command{out: &out, costs: fabricacost.Global}
	c.printDryRun(referencedProfilePlan(), nil)

	got := out.String()
	if !strings.Contains(got, "Agent Instance Profile: workload (pre-created, not managed)") {
		t.Errorf("dry-run output = %q, want the pre-created profile row", got)
	}
	if strings.Contains(got, "Agent IAM Role:") {
		t.Errorf("dry-run output = %q, want no agent IAM role row for a referenced profile", got)
	}
}

// A managed agent profile whose create fails stops the apply with that error.
func TestApplyCreateManagedProfileFailureStops(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coord"}})
	c := command{
		out: &out,
		createResource: func(_ context.Context, r *cloud.Resource) error {
			if r.TypeName == cloud.TypeAWSIAMInstanceProfile {
				return errors.New("access denied")
			}
			r.Identifier = "res-" + r.TypeName
			return nil
		},
		writeState: func(*fabricastate.State) error { return nil },
	}
	plan := agentsPlanForPrint()
	err := c.applyCreate(context.Background(), st, plan)
	if err == nil || !strings.Contains(err.Error(), "creating agent instance profile") {
		t.Fatalf("applyCreate error = %v, want a creating agent instance profile error", err)
	}
}

// A referenced profile that cannot be recorded in state stops the apply.
func TestApplyCreateReferencedProfileWriteFailureStops(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coord"}})
	c := command{
		out: &out,
		createResource: func(_ context.Context, r *cloud.Resource) error {
			r.Identifier = "res-" + r.TypeName
			return nil
		},
		writeState: func(s *fabricastate.State) error {
			m := s.GetModule("horde")
			if m != nil && len(m.Resources) > 0 && m.Resources[len(m.Resources)-1].TypeName == cloud.TypeAWSIAMInstanceProfile {
				return errors.New("disk full")
			}
			return nil
		},
	}
	err := c.applyCreate(context.Background(), st, referencedProfilePlan())
	if err == nil || !strings.Contains(err.Error(), "recording agent instance profile") {
		t.Fatalf("applyCreate error = %v, want a recording agent instance profile error", err)
	}
}
