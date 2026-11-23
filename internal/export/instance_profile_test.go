package export

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/state"
)

// A referenced profile is never declared as an IAM resource; instances carry its name.
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
	if strings.Contains(out, "AWS::IAM::InstanceProfile") {
		t.Errorf("export declares an instance profile; want a reference only:\n%s", out)
	}
	if !strings.Contains(out, "IamInstanceProfile: workload") {
		t.Errorf("export does not reference the profile by name:\n%s", out)
	}
}

// The reference lands only on resources that share the profile's role tag, so
// a coordinator in the same module is never pointed at the agent profile.
func TestBuildModuleReferencesProfileOnlyOnItsRole(t *testing.T) {
	ms := state.ModuleState{
		Name: "horde",
		Resources: []state.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "workload", Properties: map[string]string{"role": "agent", "external": "true"}},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-agent", Properties: map[string]string{"role": "agent"}},
		},
	}
	mod := buildModule(naming.Default, ms, config.Defaults(), "123456789012", "us-east-1")

	if len(mod.Resources) != 2 {
		t.Fatalf("exported %d resources, want the coordinator and agent instances (no profile)", len(mod.Resources))
	}
	if _, ok := mod.Resources[0].Properties["IamInstanceProfile"]; ok {
		t.Error("coordinator was pointed at the agent profile")
	}
	if mod.Resources[1].Properties["IamInstanceProfile"] != "workload" {
		t.Errorf("agent IamInstanceProfile = %v, want workload", mod.Resources[1].Properties["IamInstanceProfile"])
	}
}
