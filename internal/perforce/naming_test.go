package perforce

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

// acme is a non-default prefix.
var acme = naming.MustResolve("acme")

const acmeAdminParam = "/acme/perforce/0123456789abcdef0123456789abcdef/admin-password"

func TestNewCreatePlanNamesFollowPrefix(t *testing.T) {
	plan, err := NewCreatePlan(context.Background(), acme, config.PerforceConfig{}, "123456789012", "us-east-1", DefaultHelixVersion, nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	got := []string{plan.SGName, plan.InstanceName, plan.RoleName, plan.InstanceProfileName}
	want := []string{"acme-perforce-sg", "acme-perforce", "acme-perforce-role", "acme-perforce-profile"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("name %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRoleDesiredStateFollowsPrefix(t *testing.T) {
	plan := &CreatePlan{
		Prefix:                 acme,
		Account:                "123456789012",
		Region:                 "us-east-1",
		RoleName:               "acme-perforce-role",
		AdminPasswordParameter: acmeAdminParam,
		BackupS3Export:         true,
		BackupS3Bucket:         "acme-backups",
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	for _, want := range []string{"acme-ssm-output", "acme-perforce-admin-password", "acme-perforce-backup-s3", "log-group:/acme/ssm/*", acmeAdminParam} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("role document missing %q", want)
		}
	}
	if strings.Contains(string(raw), "fabrica-") {
		t.Errorf("role document still carries a fabrica name: %s", raw)
	}
}

func TestInstanceNameTagFollowsPlan(t *testing.T) {
	plan := &CreatePlan{Prefix: acme, Region: "us-east-1", Account: "123456789012", InstanceName: "acme-perforce", SubnetID: "subnet-1", InstanceType: DefaultInstanceType, VolumeSize: 500, RootDeviceName: "/dev/sda1"}
	raw, err := InstanceDesiredState(plan, "sg-1", "#cloud-config", "acme-perforce-profile", "ami-1")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	if !strings.Contains(string(raw), `"Value":"acme-perforce"`) {
		t.Errorf("instance Name tag does not carry the plan name: %s", raw)
	}
}

func TestUserDataScopesAdminParameterToPrefix(t *testing.T) {
	if _, err := GenerateRaw(UserDataConfig{Region: "us-east-1", AdminPasswordParameter: acmeAdminParam, NamePrefix: acme}); err != nil {
		t.Fatalf("prefixed parameter rejected: %v", err)
	}
	if _, err := GenerateRaw(UserDataConfig{Region: "us-east-1", AdminPasswordParameter: acmeAdminParam}); err == nil {
		t.Fatal("default prefix accepted a parameter under /acme/")
	}
}
