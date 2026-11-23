package create

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/instanceprofile"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

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
