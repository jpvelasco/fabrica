package drift

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestExtraUsesRecordedOwnership(t *testing.T) {
	for _, owner := range []string{"fabrica", "Studio-Tools", "invalid owner"} {
		for _, fetch := range []bool{false, true} {
			st := state.NewState("", "")
			st.ManagedBy = owner
			engine := &Engine{State: st}
			for _, liveOwner := range []string{"fabrica", "Studio-Tools", "other", ""} {
				for _, agent := range []bool{false, true} {
					tagList := []map[string]string{{"Key": "ManagedBy", "Value": liveOwner}}
					if agent {
						tagList = append(tagList, map[string]string{"Key": "FabricaRole", "Value": "agent"})
					}
					raw, err := json.Marshal(map[string]any{"Tags": tagList})
					if err != nil {
						t.Fatal(err)
					}
					res := cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "orphan", ActualState: raw}
					if fetch {
						res.ActualState = nil
					}
					get := func(_ context.Context, r *cloud.Resource) error { r.ActualState = raw; return nil }
					got := engine.extraResults(t.Context(), res.TypeName, []cloud.Resource{res}, nil, "horde", get)
					want := 0
					if liveOwner == owner && owner != "invalid owner" && !agent {
						want = 1
					}
					if len(got) != want {
						t.Fatalf("recorded=%q live=%q fetched=%t agent=%t: got %v", owner, liveOwner, fetch, agent, got)
					}
				}
			}
		}
	}
}
