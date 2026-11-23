package drift

import (
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
)

// A referenced profile is never looked up, so drift never reports it missing or extra.
func TestRun_ExternalProfileIsNotLookedUp(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2025.2", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "workload", Properties: map[string]string{"external": "true"}},
	})
	looked := 0
	engine := &Engine{
		State:       st,
		ResourceGet: func(context.Context, *cloud.Resource) error { looked++; return cloud.ErrResourceNotFound },
		ResourceList: func(_ context.Context, typeName string) ([]cloud.Resource, error) {
			return []cloud.Resource{{TypeName: typeName, Identifier: "workload"}}, nil
		},
	}
	report := engine.Run(context.Background())
	if looked != 0 || report.Missing != 0 || report.Extra != 0 {
		t.Errorf("looked=%d Missing=%d Extra=%d, want no lookups and no findings", looked, report.Missing, report.Extra)
	}
}
