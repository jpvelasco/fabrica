package teardown

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// A pre-created profile is never deleted, even when a spec order names it.
func TestRunNeverDeletesExternalProfile(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-abc123"},
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "workload", Properties: map[string]string{"external": "true"}},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-abc123"},
	})
	var deleted []string
	c := newTestCommand(&bytes.Buffer{}, st, nil)
	c.AssumeYes = true
	c.Spec.ResourceOrder = func(*fabricastate.ModuleState) []cloud.Resource {
		return []cloud.Resource{{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "workload"}, {TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-abc123"}, {TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-abc123"}}
	}
	c.DeleteResource = func(_ context.Context, r *cloud.Resource) error {
		deleted = append(deleted, r.TypeName)
		return nil
	}
	if err := c.Run(context.Background()); err != nil || strings.Contains(strings.Join(deleted, ","), "InstanceProfile") || len(deleted) != 2 {
		t.Errorf("Run err=%v deleted=%v; want the instance and security group only", err, deleted)
	}
}
