package ci

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/iamrole"
)

func TestNoInlinePolicyDocument(t *testing.T) {
	data, err := os.ReadFile("buildspec.go")
	if err != nil {
		t.Fatalf("read buildspec.go: %v", err)
	}
	if strings.Contains(string(data), "inlinePolicyDocument") {
		t.Fatalf("buildspec.go still contains inlinePolicyDocument — delete the wrapper, buildspec.go should contain only buildspec")
	}
}

func TestRoleDesiredState_MatchesRoleDocument(t *testing.T) {
	plan := testPlan()
	gotRaw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(gotRaw, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	wantRaw, err := iamrole.RoleDocument(plan.RoleName, iamrole.ServiceCodeBuild, nil, []map[string]any{iamrole.CICodeBuildInlinePolicy(plan.Region, plan.Account, plan.ProjectName)}, map[string]string{"FabricaModule": "ci"})
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
	// Verify trust service and tags
	if !strings.Contains(string(gotJSON), iamrole.ServiceCodeBuild) {
		t.Errorf("RoleDesiredState missing trust service %q", iamrole.ServiceCodeBuild)
	}
	if !strings.Contains(string(gotJSON), `"FabricaModule"`) {
		t.Errorf("RoleDesiredState missing FabricaModule tag — should use RoleDocument with FabricaModule=ci")
	}
}
