//go:build integration

// Integration test for the real CI resources (IAM role + CodeBuild project) via
// Cloud Control, plus the CodeBuildRunner surface.
//
// Run with real AWS credentials:
//
//	FABRICA_INTEGRATION=1 FABRICA_INTEGRATION_ACCOUNT=<acct> AWS_REGION=us-west-2 \
//	  go test -tags integration ./internal/cloud/aws/ -run TestIntegrationCI -v
//
// It creates a uniquely-named IAM role and CodeBuild project, asserts they can
// be read back, and unconditionally deletes both via t.Cleanup. It does NOT run
// a build (that needs a live Horde coordinator).
package aws

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/internal/ci"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestIntegrationCI(t *testing.T) {
	if os.Getenv("FABRICA_INTEGRATION") != "1" {
		t.Skip("set FABRICA_INTEGRATION=1 to run real-AWS integration tests")
	}
	wantAccount := os.Getenv("FABRICA_INTEGRATION_ACCOUNT")
	if wantAccount == "" {
		t.Fatal("FABRICA_INTEGRATION_ACCOUNT must be set to guard against the wrong account")
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-west-2"
	}

	cfg := config.Defaults()
	cfg.Cloud.AWS.Region = region
	prov, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}

	ctx := context.Background()
	account, _, _, err := prov.Identity(ctx)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if account != wantAccount {
		t.Fatalf("refusing to run: live account %s != FABRICA_INTEGRATION_ACCOUNT %s", account, wantAccount)
	}

	suffix := time.Now().UTC().Format("20060102150405")
	cicfg := config.CIConfig{ProjectName: "fabrica-ci-it-" + suffix}
	plan := ci.NewCreatePlan(cicfg, account, region, "http://10.0.0.1:5000")
	plan.RoleName = "fabrica-ci-it-role-" + suffix

	rc := prov.Resources()
	runner, ok := prov.(fabricac.CodeBuildRunner)
	if !ok {
		t.Fatal("provider does not implement CodeBuildRunner")
	}

	roleState, err := ci.RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	role := &fabricac.Resource{TypeName: ci.TypeAWSIAMRole, DesiredState: roleState}
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", account, plan.RoleName)

	t.Cleanup(func() {
		// Best-effort unconditional cleanup, project first (depends on the role).
		_ = runner.DeleteProject(context.Background(), plan.ProjectName)
		if role.Identifier != "" {
			_ = rc.Delete(context.Background(), &fabricac.Resource{TypeName: ci.TypeAWSIAMRole, Identifier: role.Identifier})
		}
	})

	// IAM role via Cloud Control (supported); CodeBuild project via the SDK
	// runner (AWS::CodeBuild::Project has no Cloud Control CREATE action).
	if err := rc.Create(ctx, role); err != nil {
		t.Fatalf("creating IAM role: %v", err)
	}
	// IAM role propagation can lag before CodeBuild accepts it as a service role.
	time.Sleep(10 * time.Second)

	created, err := runner.EnsureProject(ctx, ci.ProjectSpec(plan, roleARN))
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if !created {
		t.Error("EnsureProject created = false on first create")
	}

	// Idempotency: second EnsureProject must be a no-op.
	created2, err := runner.EnsureProject(ctx, ci.ProjectSpec(plan, roleARN))
	if err != nil {
		t.Fatalf("second EnsureProject: %v", err)
	}
	if created2 {
		t.Error("second EnsureProject created = true, want false")
	}
}
