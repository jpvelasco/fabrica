package drift

// Per-module full-contract rebuild edge cases: the fallback and
// fail-closed branches of the perforce instance rebuild that the
// happy-path and missing-input tests do not reach.

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/state"
)

// decodedUserData decodes the base64 UserData field of a rebuilt instance's
// desired state so tests can assert on the rendered cloud-init.
func decodedUserData(t *testing.T, ds json.RawMessage) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	raw, ok := m["UserData"].(string)
	if !ok {
		t.Fatalf("expected a UserData field, got %v", m["UserData"])
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("UserData is not base64: %v", err)
	}
	return string(decoded)
}

func perforceModuleFixture() *state.ModuleState {
	return &state.ModuleState{
		Name:    "perforce",
		Version: "2025.2",
		Resources: []state.ModuleResource{
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-pf"},
			{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "fabrica-perforce-profile"},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-pf", Properties: map[string]string{"imageId": "ami-pf"}},
		},
	}
}

// volumeSizeFor resolves recorded property, then config, then the module
// default; a malformed recorded value falls through to the config value.
func TestVolumeSizeFor_ResolvesInOrder(t *testing.T) {
	if got := volumeSizeFor(&state.ModuleResource{Properties: map[string]string{"volumeSize": "512"}}, 200, 500); got != 512 {
		t.Errorf("expected the recorded volume size 512, got %d", got)
	}
	if got := volumeSizeFor(&state.ModuleResource{Properties: map[string]string{"volumeSize": "not-a-size"}}, 200, 500); got != 200 {
		t.Errorf("expected the config volume size 200 after a malformed recorded value, got %d", got)
	}
	if got := volumeSizeFor(&state.ModuleResource{}, 200, 500); got != 200 {
		t.Errorf("expected the config volume size 200 without a recorded value, got %d", got)
	}
	if got := volumeSizeFor(&state.ModuleResource{}, 0, 500); got != 500 {
		t.Errorf("expected the module default 500, got %d", got)
	}
}

// Perforce rebuild with no module version takes the pinned Helix version
// and the sizing from the module config when the instance recorded no
// properties of its own.
func TestRebuildInstanceDesiredState_Perforce_VersionFromConfig(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.Version = "2024.2"
	cfg.Perforce.InstanceType = "m7i.large"

	mod := perforceModuleFixture()
	mod.Version = ""
	withPerforcePassword(t, func(string) (string, error) { return "p4-admin", nil })

	ds, err := rebuildInstanceDesiredState(&mod.Resources[2], mod, cfg)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	ud := decodedUserData(t, ds)
	if !strings.Contains(ud, "2024.2") {
		t.Errorf("cloud-init must pin the configured Helix version 2024.2, got:\n%s", ud)
	}
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if m["InstanceType"] != "m7i.large" {
		t.Errorf("expected the configured instance type m7i.large, got %v", m["InstanceType"])
	}
}

// Perforce rebuild with neither a module version nor a config version
// falls back to the module's default pin.
func TestRebuildInstanceDesiredState_Perforce_VersionDefault(t *testing.T) {
	mod := perforceModuleFixture()
	mod.Version = ""
	withPerforcePassword(t, func(string) (string, error) { return "p4-admin", nil })

	ds, err := rebuildInstanceDesiredState(&mod.Resources[2], mod, config.Defaults())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	ud := decodedUserData(t, ds)
	if !strings.Contains(ud, perforce.DefaultHelixVersion) {
		t.Errorf("cloud-init must pin the default Helix version %s, got:\n%s", perforce.DefaultHelixVersion, ud)
	}
}

// A Perforce module with no recorded security group is unsupported: the
// rebuild says so instead of launching an instance that cannot join the VPC.
func TestRebuildInstanceDesiredState_Perforce_MissingSecurityGroup(t *testing.T) {
	mod := perforceModuleFixture()
	mod.Resources = mod.Resources[1:] // drop the security group
	_, err := rebuildInstanceDesiredState(&mod.Resources[1], mod, config.Defaults())
	if err == nil || !strings.Contains(err.Error(), "security group") {
		t.Fatalf("expected a security group error, got %v", err)
	}
}

// An empty admin password must fail the rebuild with a render error, not
// launch an instance with an unusable password.
func TestRebuildInstanceDesiredState_Perforce_EmptyAdminPassword(t *testing.T) {
	mod := perforceModuleFixture()
	withPerforcePassword(t, func(string) (string, error) { return "", nil })

	_, err := rebuildInstanceDesiredState(&mod.Resources[2], mod, config.Defaults())
	if err == nil || !strings.Contains(err.Error(), "rendering perforce cloud-init") {
		t.Fatalf("expected a cloud-init render error, got %v", err)
	}
}
