package teardown

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// externalProfileState records a module whose instance profile was pre-created.
func externalProfileState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-abc123"},
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "workload", Properties: map[string]string{"external": "true"}},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-abc123"},
	})
	return st
}

// A spec ResourceOrder that names the referenced profile must still not delete
// it: the filter applies to every order, so destroy never touches the profile.
func TestRunNeverDeletesExternalProfile(t *testing.T) {
	var out bytes.Buffer
	var deleted []string
	c := newTestCommand(&out, externalProfileState(), nil)
	c.AssumeYes = true
	c.Spec.ResourceOrder = func(*fabricastate.ModuleState) []cloud.Resource {
		return []cloud.Resource{
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-abc123"},
			{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "workload"},
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-abc123"},
		}
	}
	c.DeleteResource = func(_ context.Context, r *cloud.Resource) error {
		deleted = append(deleted, r.TypeName)
		return nil
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Join(deleted, ",") != "AWS::EC2::Instance,AWS::EC2::SecurityGroup" {
		t.Errorf("deleted = %v, want the instance and security group only", deleted)
	}
}

func TestDryRunShowsKeptExternalProfile(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, externalProfileState(), nil)
	c.DryRun = true

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "kept: AWS::IAM::InstanceProfile workload (pre-created, not managed)") {
		t.Errorf("dry-run output = %q, want the kept pre-created profile line", out.String())
	}
}
