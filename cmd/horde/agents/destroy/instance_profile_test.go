package destroy

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// referencedAgentPool is an agent pool whose instance profile was pre-created.
func referencedAgentPool() (*fabricastate.State, *fabricastate.ModuleState) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coord"},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-agent", Properties: map[string]string{"role": "agent"}},
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "workload", Properties: map[string]string{"role": "agent", "external": "true"}},
		{TypeName: cloud.TypeAWSEC2LaunchTemplate, Identifier: "lt-agent", Properties: map[string]string{"role": "agent"}},
	})
	return st, st.GetModule("horde")
}

func TestAgentsToDeleteSkipsReferencedProfile(t *testing.T) {
	_, m := referencedAgentPool()
	for _, r := range agentsToDelete(m) {
		if r.TypeName == cloud.TypeAWSIAMInstanceProfile {
			t.Fatalf("agentsToDelete includes the referenced profile %q; destroy must never delete it", r.Identifier)
		}
	}
}

func TestApplyDestroyNeverDeletesReferencedProfile(t *testing.T) {
	st, m := referencedAgentPool()
	var deleted []string
	c := command{
		out: &bytes.Buffer{},
		deleteResource: func(_ context.Context, r *cloud.Resource) error {
			deleted = append(deleted, r.TypeName)
			return nil
		},
		writeState: func(*fabricastate.State) error { return nil },
	}

	if err := c.apply(context.Background(), st, m, agentsToDelete(m)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, typeName := range deleted {
		if typeName == cloud.TypeAWSIAMInstanceProfile || typeName == cloud.TypeAWSIAMRole {
			t.Errorf("deleted %s, want the referenced profile left alone", typeName)
		}
	}
	// The pool is gone, so the record of the referenced profile is dropped too:
	// the module must no longer read as an agent pool.
	if agentsProvisioned(m) {
		t.Errorf("agents still provisioned after destroy: %+v", m.Resources)
	}
}
