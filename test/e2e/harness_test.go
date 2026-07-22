package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/root"
	"github.com/jpvelasco/fabrica/internal/state"
)

// runCLI builds a fresh real root command (fresh globals.Store) with a captured
// buffer, runs the given args, and returns combined output + the command error.
// A fresh root per call means PersistentPreRunE re-Inits from the temp
// fabrica.yaml — and resolves the "fake" provider backed by currentFake.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := root.New(&buf)
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// setupE2E isolates the test: a fresh temp working dir, a fake-provider config,
// and a fresh in-memory store. Returns the store for failure-injection cases.
func setupE2E(t *testing.T) *fakeStore {
	t.Helper()
	t.Chdir(t.TempDir())
	writeConfig(t)
	return resetFake(t)
}

// writeConfig emits a fabrica.yaml selecting the fake provider. VPC/subnet are
// set so provisioning is fully deterministic (create passes resolver=nil, so
// empty values are also fine, but explicit is clearer).
func writeConfig(t *testing.T) {
	t.Helper()
	const cfg = `cloud:
  provider: fake
  aws:
    region: us-east-1
    accountId: "123456789012"
state:
  bucket: fabrica-state-123456789012
  table: fabrica-state-lock
perforce:
  vpcId: vpc-fake
  subnetId: subnet-fake
horde:
  amiId: ami-fake
  vpcId: vpc-fake
  subnetId: subnet-fake
lore:
  amiId: ami-fake
  vpcId: vpc-fake
  subnetId: subnet-fake
ddc:
  amiId: ami-fake
  vpcId: vpc-fake
  subnetId: subnet-fake
workstation:
  amiId: ami-fake
  vpcId: vpc-fake
  subnetId: subnet-fake
deploy:
  buildBucket: fake-build-bucket
`
	if err := os.WriteFile("fabrica.yaml", []byte(cfg), 0600); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
}

// readState loads .fabrica/state.json; fails the test if it is missing.
func readState(t *testing.T) *state.State {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(".fabrica", "state.json"))
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	var st state.State
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("readState unmarshal: %v", err)
	}
	return &st
}

func assertContains(t *testing.T, out, substr string) {
	t.Helper()
	if !strings.Contains(out, substr) {
		t.Fatalf("output missing %q:\n%s", substr, out)
	}
}

func assertJSON(t *testing.T, out string, target any) {
	t.Helper()
	// The command may print a human line before/after JSON in some paths; find
	// the JSON object/array span. For these flows the JSON is the whole output.
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), target); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
}

func assertModuleExists(t *testing.T, st *state.State, name string) {
	t.Helper()
	if st.GetModule(name) == nil {
		t.Fatalf("expected module %q in state, modules present: %v", name, moduleNames(st))
	}
}

func assertModuleAbsent(t *testing.T, st *state.State, name string) {
	t.Helper()
	if st.GetModule(name) != nil {
		t.Fatalf("expected module %q absent, but it is present", name)
	}
}

func assertModuleStatus(t *testing.T, st *state.State, name, want string) {
	t.Helper()
	m := st.GetModule(name)
	if m == nil {
		t.Fatalf("module %q not in state", name)
	}
	if m.Status != want {
		t.Fatalf("module %q status = %q, want %q", name, m.Status, want)
	}
}

func assertResourceType(t *testing.T, st *state.State, module, typeName string) {
	t.Helper()
	m := st.GetModule(module)
	if m == nil {
		t.Fatalf("module %q not in state", module)
	}
	for _, r := range m.Resources {
		if r.TypeName == typeName {
			return
		}
	}
	t.Fatalf("module %q has no resource of type %q; has: %v", module, typeName, m.Resources)
}

// assertEC2ModuleLifecycle runs create → aggregate status → cost report → destroy
// for an EC2+SG module (perforce, lore, …). provisionedMsg is a substring of
// the create success line.
func assertEC2ModuleLifecycle(t *testing.T, module, provisionedMsg string) {
	t.Helper()
	setupE2E(t)

	out, err := runCLI(t, module, "create", "--yes")
	if err != nil {
		t.Fatalf("%s create: %v\n%s", module, err, out)
	}
	assertContains(t, out, provisionedMsg)

	st := readState(t)
	assertModuleExists(t, st, module)
	assertResourceType(t, st, module, "AWS::EC2::Instance")
	assertResourceType(t, st, module, "AWS::EC2::SecurityGroup")

	out, err = runCLI(t, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	assertContains(t, out, module)

	out, err = runCLI(t, "cost", "report")
	if err != nil {
		t.Fatalf("cost report: %v\n%s", err, out)
	}
	assertContains(t, out, module)
	assertContains(t, out, "Total:")

	out, err = runCLI(t, module, "destroy", "--yes")
	if err != nil {
		t.Fatalf("%s destroy: %v\n%s", module, err, out)
	}
	st = readState(t)
	assertModuleAbsent(t, st, module)
}

func moduleNames(st *state.State) []string {
	var names []string
	for _, m := range st.Modules {
		names = append(names, m.Name)
	}
	return names
}

// TestHarnessSmoke verifies the harness wiring: the fake provider resolves and a
// trivial command runs end-to-end.
func TestHarnessSmoke(t *testing.T) {
	setupE2E(t)
	out, err := runCLI(t, "version")
	if err != nil {
		t.Fatalf("version: %v\n%s", err, out)
	}
	// version prints the version string; just assert it ran and produced output.
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected version output, got empty")
	}
}

// TestHarnessFakeResolves confirms PersistentPreRunE resolves the fake provider
// (a command that needs the provider runs without an AWS error).
func TestHarnessFakeResolves(t *testing.T) {
	setupE2E(t)
	out, err := runCLI(t, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	// Fresh account: no backend, no modules. cmd/status prints this exact line
	// (cmd/status/status.go): "Nothing provisioned yet, and your state backend
	// isn't set up."
	assertContains(t, out, "Nothing provisioned yet")
}
