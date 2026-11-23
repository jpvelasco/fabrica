package drift

import (
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
)

// A pre-created instance profile is never looked up, so drift never reports it
// missing or extra, even when a lookup would say it is gone.
func TestRun_ExternalProfileIsNotLookedUp(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2025.2", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-123"},
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "workload", Properties: map[string]string{"external": "true"}},
	})

	var looked []string
	engine := &Engine{
		State: st,
		ResourceGet: func(_ context.Context, r *cloud.Resource) error {
			looked = append(looked, r.TypeName)
			return nil
		},
		ResourceList: func(_ context.Context, typeName string) ([]cloud.Resource, error) {
			if typeName == cloud.TypeAWSIAMInstanceProfile {
				return []cloud.Resource{{TypeName: typeName, Identifier: "workload"}}, nil
			}
			return nil, nil
		},
	}

	report := engine.Run(context.Background())

	for _, typeName := range looked {
		if typeName == cloud.TypeAWSIAMInstanceProfile {
			t.Errorf("drift looked up the referenced profile; it should be left alone")
		}
	}
	if report.Missing != 0 || report.Extra != 0 {
		t.Errorf("Missing=%d Extra=%d, want 0 and 0", report.Missing, report.Extra)
	}
	for _, r := range report.Modules[0].Resources {
		if r.TypeName == cloud.TypeAWSIAMInstanceProfile && r.Status != InSync {
			t.Errorf("profile status = %s, want in sync", r.Status)
		}
	}
}
