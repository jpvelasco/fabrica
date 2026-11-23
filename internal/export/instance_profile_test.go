package export

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/state"
)

// A referenced profile is never declared as an IAM resource; the instance carries its name.
func TestCloudFormationExportReferencesExternalProfile(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.Modules = append(st.Modules, state.ModuleState{
		Name: "perforce",
		Resources: []state.ModuleResource{
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-1"},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "workload", Properties: map[string]string{"external": "true"}},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
		},
	})

	data, err := GenerateOutput(naming.Default, CloudFormation, st, config.Defaults())
	if err != nil {
		t.Fatalf("GenerateOutput: %v", err)
	}
	out := string(data)
	if strings.Contains(out, "AWS::IAM::InstanceProfile") || !strings.Contains(out, "IamInstanceProfile: workload") {
		t.Errorf("want a profile reference and no declared instance profile:\n%s", out)
	}
}

// The reference lands only on resources sharing the profile's role tag, so the
// horde coordinator is never pointed at the agent profile; the agent launch template is.
func TestBuildModuleReferencesProfileOnlyOnItsRole(t *testing.T) {
	ms := state.ModuleState{
		Name: "horde",
		Resources: []state.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "workload", Properties: map[string]string{"role": "agent", "external": "true"}},
			{TypeName: "AWS::EC2::LaunchTemplate", Identifier: "lt-agent", Properties: map[string]string{"role": "agent"}},
		},
	}
	mod := buildModule(naming.Default, ms, config.Defaults(), "123456789012", "us-east-1")
	if len(mod.Resources) != 2 {
		t.Fatalf("exported %d resources, want the coordinator and launch template (no profile)", len(mod.Resources))
	}
	if _, ok := mod.Resources[0].Properties["IamInstanceProfile"]; ok {
		t.Error("coordinator was pointed at the agent profile")
	}
	data, _ := mod.Resources[1].Properties["LaunchTemplateData"].(map[string]any)
	if profile, _ := data["IamInstanceProfile"].(map[string]any); profile["Name"] != "workload" {
		t.Errorf("launch template IamInstanceProfile = %v, want Name workload", data["IamInstanceProfile"])
	}
}
