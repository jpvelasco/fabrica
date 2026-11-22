package drift

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/state"
)

// acmePrefix bypasses the config gate (#705) to exercise a non-default prefix.
var acmePrefix = naming.MustResolve("acme")

// acmeAdminParam is an SSM parameter name, not a credential. #nosec G101
const acmeAdminParam = "/acme/perforce/0123456789abcdef0123456789abcdef/admin-password"

func TestRebuildPerforceFollowsPrefix(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.SubnetId = "subnet-pf"
	st := state.NewState("123456789012", "us-east-1")
	mod := &state.ModuleState{
		Name:    "perforce",
		Version: "2024.2",
		Resources: []state.ModuleResource{
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-pf"},
			{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "acme-perforce-profile"},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-pf", Properties: map[string]string{
				"imageId":                "ami-perforce-123",
				"rootDevice":             "/dev/sda1",
				"adminPasswordParameter": acmeAdminParam,
			}},
		},
	}

	ds, err := rebuildInstanceDesiredState(context.Background(), acmePrefix, &mod.Resources[2], mod, st, cfg, nil)
	if err != nil {
		t.Fatalf("rebuild instance: %v", err)
	}
	if !strings.Contains(string(ds), `"Value":"acme-perforce"`) {
		t.Errorf("rebuilt instance Name tag is not acme-perforce: %s", ds)
	}
	if ud := decodedUserData(t, ds); !strings.Contains(ud, "/acme/perforce/") {
		t.Errorf("rebuilt user data does not read the prefixed admin parameter:\n%s", ud)
	}

	sg, err := rebuildSGDesiredState(acmePrefix, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "perforce"}, cfg)
	if err != nil {
		t.Fatalf("rebuild security group: %v", err)
	}
	if !strings.Contains(string(sg), `"GroupName":"acme-perforce-sg"`) {
		t.Errorf("rebuilt security group is not acme-perforce-sg: %s", sg)
	}
}

// WithNamePrefix carries the prefix from the command layer into the remediation
// plan, so a missing instance is recreated under it.
func TestPlanRemediationUsesWithNamePrefix(t *testing.T) {
	cfg := config.Defaults()
	cfg.Lore.StoreBackend = "s3"
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("lore", "ami-lore", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-lore"},
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "acme-lore-profile"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-lore", Properties: map[string]string{"rootDevice": "/dev/sda1"}},
	})
	report := &DriftReport{
		Modules: []ModuleDrift{{Name: "lore", Resources: []DriftResult{
			{Module: "lore", TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-lore", Status: Missing},
		}}},
		Missing: 1,
	}

	plan := PlanRemediation(context.Background(), report, st, cfg, WithNamePrefix(acmePrefix))
	if len(plan.Actions) != 1 || plan.Actions[0].Resource == nil {
		t.Fatalf("expected one create action for the missing instance, got %+v", plan.Actions)
	}
	if ds := string(plan.Actions[0].Resource.DesiredState); !strings.Contains(ds, `"Value":"acme-lore"`) {
		t.Errorf("remediated instance Name tag is not acme-lore: %s", ds)
	}
}

func TestRebuildLoreFollowsPrefix(t *testing.T) {
	cfg := config.Defaults()
	cfg.Lore.StoreBackend = "s3"
	st := state.NewState("123456789012", "us-east-1")
	mod := &state.ModuleState{
		Name:    "lore",
		Version: "ami-lore",
		Resources: []state.ModuleResource{
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-lore"},
			{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "acme-lore-profile"},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-lore", Properties: map[string]string{"rootDevice": "/dev/sda1"}},
		},
	}

	ds, err := rebuildInstanceDesiredState(context.Background(), acmePrefix, &mod.Resources[2], mod, st, cfg, nil)
	if err != nil {
		t.Fatalf("rebuild instance: %v", err)
	}
	if !strings.Contains(string(ds), `"Value":"acme-lore"`) {
		t.Errorf("rebuilt instance Name tag is not acme-lore: %s", ds)
	}
	if ud := decodedUserData(t, ds); !strings.Contains(ud, "acme-lore-store-123456789012-us-east-1") {
		t.Errorf("rebuilt user data does not carry the prefixed store bucket:\n%s", ud)
	}

	sg, err := rebuildSGDesiredState(acmePrefix, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "lore"}, cfg)
	if err != nil {
		t.Fatalf("rebuild security group: %v", err)
	}
	if !strings.Contains(string(sg), `"GroupName":"acme-lore-sg"`) {
		t.Errorf("rebuilt security group is not acme-lore-sg: %s", sg)
	}
}

// acmeWSParam is an SSM parameter name, not a credential. #nosec G101
const acmeWSParam = "/acme/workstation/0123456789abcdef0123456789abcdef/dcv-password"

func TestRebuildWorkstationFollowsPrefix(t *testing.T) {
	cfg := config.Defaults()
	st := state.NewState("123456789012", "us-east-1")
	mod := &state.ModuleState{
		Name:    "workstation",
		Version: "ami-ws",
		Resources: []state.ModuleResource{
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-ws"},
			{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "acme-workstation-profile"},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ws", Properties: map[string]string{
				"sessionPasswordParameter": acmeWSParam,
				"mountPerforce":            "false",
			}},
		},
	}

	ds, err := rebuildInstanceDesiredState(context.Background(), acmePrefix, &mod.Resources[2], mod, st, cfg, fakeImageInspector(t, "/dev/sda1", "ebs"))
	if err != nil {
		t.Fatalf("rebuild instance: %v", err)
	}
	if !strings.Contains(string(ds), `"Value":"acme-workstation"`) {
		t.Errorf("rebuilt instance Name tag is not acme-workstation: %s", ds)
	}
	if ud := decodedUserData(t, ds); !strings.Contains(ud, acmeWSParam) {
		t.Errorf("rebuilt user data does not read the prefixed session parameter:\n%s", ud)
	}

	sg, err := rebuildSGDesiredState(acmePrefix, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "workstation"}, cfg)
	if err != nil {
		t.Fatalf("rebuild security group: %v", err)
	}
	if !strings.Contains(string(sg), `"GroupName":"acme-workstation-sg"`) {
		t.Errorf("rebuilt security group is not acme-workstation-sg: %s", sg)
	}
}
