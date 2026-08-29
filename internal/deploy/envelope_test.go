package deploy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/iamrole"
)

func TestRoleDesiredState_MatchesRoleDocument(t *testing.T) {
	plan := &SetupPlan{Account: "123456789012", Region: "us-west-2", RoleName: "fabrica-deploy-gamelift", AliasName: "a", BuildBucket: "test-bucket"}
	gotRaw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(gotRaw, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	wantRaw, err := iamrole.RoleDocument(plan.RoleName, iamrole.ServiceGameLift, nil, []map[string]any{iamrole.DeployS3ReadPolicy(plan.BuildBucket)}, map[string]string{"FabricaModule": "deploy"})
	if err != nil {
		t.Fatalf("RoleDocument: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("RoleDesiredState JSON mismatch:\n got %s\nwant %s", gotJSON, wantJSON)
	}
	if !strings.Contains(string(gotJSON), iamrole.ServiceGameLift) {
		t.Errorf("RoleDesiredState missing trust service %q", iamrole.ServiceGameLift)
	}
}
