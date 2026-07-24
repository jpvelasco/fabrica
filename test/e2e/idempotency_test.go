package e2e

import (
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
)

// TestPerforceCreate_AlreadyExists verifies that when a Perforce resource
// already exists on AWS (simulating partial-failure recovery where Create
// succeeded but WriteState failed), the create command recovers the existing
// resource and completes successfully instead of failing.
func TestPerforceCreate_AlreadyExists(t *testing.T) {
	store := setupE2E(t)

	// Simulate: a previous run created the SG on AWS, but WriteState failed
	// before recording it. The next run should recover the existing resource.
	store.failCreateType = "AWS::EC2::SecurityGroup"
	store.failCreateID = "sg-existing-from-previous-run"

	out, err := runCLI(t, "perforce", "create", "--yes")
	if err != nil {
		t.Fatalf("perforce create should succeed recovering existing resource: %v\noutput: %s", err, out)
	}
	assert.Contains(t, out, "Security group created")
	assert.Contains(t, out, "sg-existing-from-previous-run")

	// Verify state was written with the recovered identifier.
	st := readState(t)
	m := st.GetModule("perforce")
	if m == nil {
		t.Fatal("perforce module not in state after create")
	}
	if len(m.Resources) == 0 {
		t.Fatal("perforce module has no resources in state")
	}
	if m.Resources[0].Identifier != "sg-existing-from-previous-run" {
		t.Errorf("first resource identifier = %q, want recovered sg-existing-from-previous-run", m.Resources[0].Identifier)
	}
}

// TestDeploySetup_AlreadyExists verifies that deploy setup recovers when the
// IAM role already exists on AWS (the scenario that caused the original E2E
// failure — waiter state transitioned to Failure).
func TestDeploySetup_AlreadyExists(t *testing.T) {
	store := setupE2E(t)

	cfgContent := `cloud:
  provider: fake
  aws:
    region: us-east-1
    accountId: "123456789012"
state:
  bucket: fabrica-state-123456789012
  table: fabrica-state-lock
deploy:
  buildBucket: test-builds-123456789012
`
	if err := os.WriteFile("fabrica.yaml", []byte(cfgContent), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Cleanup(func() { os.Remove("fabrica.yaml") })

	store.failCreateType = "AWS::IAM::Role"
	store.failCreateID = "arn:aws:iam::123456789012:role/fabrica-deploy-gamelift"

	out, err := runCLI(t, "deploy", "setup", "--yes")
	if err != nil {
		t.Fatalf("deploy setup should succeed recovering existing role: %v\noutput: %s", err, out)
	}
	assert.Contains(t, out, "created IAM role")
}
