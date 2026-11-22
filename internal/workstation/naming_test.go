package workstation

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

// acme is a non-default prefix.
var acme = naming.MustResolve("acme")

const acmeSessionParam = "/acme/workstation/0123456789abcdef0123456789abcdef/dcv-password"

func TestNewCreatePlanNamesFollowPrefix(t *testing.T) {
	cfg := config.WorkstationConfig{AmiID: "ami-ws", VPCId: "vpc-1", SubnetId: "subnet-1"}
	plan, err := NewCreatePlan(context.Background(), acme, cfg, "123456789012", "us-east-1", nil, "", "")
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	got := []string{plan.SGName, plan.InstanceName, plan.RoleName, plan.InstanceProfileName}
	want := []string{"acme-workstation-sg", "acme-workstation", "acme-workstation-role", "acme-workstation-profile"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRoleDesiredStateFollowsPrefix(t *testing.T) {
	plan := &CreatePlan{
		Prefix:                   acme,
		Account:                  "123456789012",
		Region:                   "us-east-1",
		RoleName:                 "acme-workstation-role",
		SessionPasswordParameter: acmeSessionParam,
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	for _, want := range []string{"acme-ssm-output", "acme-workstation-dcv-password", "acme-workstation-dcv-license", "log-group:/acme/ssm/*", acmeSessionParam} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("role document missing %q", want)
		}
	}
	if strings.Contains(string(raw), "fabrica-") {
		t.Errorf("role document still carries a fabrica name: %s", raw)
	}
}

func TestSessionPasswordScopeFollowsPrefix(t *testing.T) {
	const nonce = "0123456789abcdef0123456789abcdef"
	name, err := SessionPasswordScope(acme).NameFor(nonce)
	if err != nil || name != acmeSessionParam {
		t.Fatalf("NameFor = %q, %v; want %q", name, err, acmeSessionParam)
	}
	if !SessionPasswordScope(acme).IsName(acmeSessionParam) {
		t.Errorf("acme scope rejects its own parameter %q", acmeSessionParam)
	}
	if SessionPasswordScope(naming.Default).IsName(acmeSessionParam) {
		t.Errorf("default scope accepts an acme parameter %q", acmeSessionParam)
	}
	if got := SessionPasswordPolicyName(acme); got != "acme-workstation-dcv-password" {
		t.Errorf("SessionPasswordPolicyName = %q", got)
	}
	if got := DCVLicensePolicyName(acme); got != "acme-workstation-dcv-license" {
		t.Errorf("DCVLicensePolicyName = %q", got)
	}
}
