package export

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/state"
)

// prefixedState records resources with no physical names, so every name in the
// export comes from the prefix-derived fallbacks.
func prefixedState() *state.State {
	st := state.NewState(exportAccount, exportRegion)
	st.UpsertModule("horde", "acme-ami", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "acme-horde-sg"},
		{TypeName: "AWS::EC2::Instance", Identifier: "acme-horde-instance", Properties: map[string]string{"instanceType": "m7i.2xlarge"}},
		{TypeName: "AWS::IAM::Role", Identifier: "acme-horde-role"},
		{TypeName: "AWS::IAM::InstanceProfile", Identifier: "acme-horde-profile"},
	})
	st.UpsertModule("ci", "", "ready", []state.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "acme-ci-codebuild"},
		{TypeName: "AWS::CodeBuild::Project", Identifier: "acme-ci"},
	})
	st.UpsertModule("deploy", "", "ready", []state.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "acme-deploy-gamelift"},
		{TypeName: "AWS::GameLift::Alias", Identifier: "acme-deploy"},
	})
	return st
}

func TestTemplateFollowsNamePrefix(t *testing.T) {
	acme := naming.MustResolve("acme")
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = exportAccount
	cfg.Deploy.BuildBucket = "acme-builds"

	out, err := GenerateOutput(acme, CloudFormation, prefixedState(), cfg)
	if err != nil {
		t.Fatalf("GenerateOutput(acme): %v", err)
	}
	text := string(out)
	for _, want := range []string{
		"GroupName: acme-horde-sg",
		"RoleName: acme-horde-role",
		"InstanceProfileName: acme-horde-profile",
		"acme-ssm-output",
		"Name: acme-horde",
		"Name: acme-ci",
		"RoleName: acme-ci-codebuild",
		"acme-ci-inline",
		"Name: acme-deploy-alias",
		"RoleName: acme-deploy-gamelift",
		"acme-deploy-s3-read",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("template missing %q", want)
		}
	}
	// The state-backend module still uses its own resolved names (#705 PR5), so
	// only the module names this test records are checked for leftovers.
	for _, leftover := range []string{"fabrica-horde", "fabrica-ci", "fabrica-deploy", "fabrica-ssm", "/fabrica/"} {
		if strings.Contains(text, leftover) {
			t.Errorf("template still contains default name %q", leftover)
		}
	}

	again, err := GenerateOutput(acme, CloudFormation, prefixedState(), cfg)
	if err != nil {
		t.Fatalf("GenerateOutput(acme) second run: %v", err)
	}
	if string(again) != text {
		t.Error("prefixed template is not deterministic across runs")
	}
}
