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
