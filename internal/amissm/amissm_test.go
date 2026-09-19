package amissm

import (
	"strings"
	"testing"
)

func TestEnsureScriptFailsClosed(t *testing.T) {
	script := EnsureScript()
	for _, want := range []string{
		DebUnit,
		SnapUnit,
		"snap install amazon-ssm-agent --classic",
		`systemctl enable "$ssm_unit"`,
		"amazon-ssm-agent is not installed",
		"cannot be enabled",
		"enabled|enabled-runtime|alias|indirect",
		"is static and will not start at boot",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("EnsureScript missing %q", want)
		}
	}
	if strings.Contains(script, "|| true") {
		t.Error("EnsureScript must not soft-fail with || true")
	}
	if strings.Contains(script, "enable amazon-ssm-agent.service ||") {
		t.Error("EnsureScript must not chain SSM enable with ||")
	}
}

func TestRequireEnabledScriptBakeVsRuntime(t *testing.T) {
	bake := RequireEnabledScript(false)
	runtime := RequireEnabledScript(true)
	for _, script := range []string{bake, runtime} {
		for _, want := range []string{
			DebUnit,
			SnapUnit,
			"amazon-ssm-agent is not installed",
			"is not enabled",
			"enabled|enabled-runtime|alias|indirect",
			"is static and will not start at boot",
		} {
			if !strings.Contains(script, want) {
				t.Errorf("RequireEnabledScript missing %q", want)
			}
		}
	}
	active := `systemctl is-active --quiet "$ssm_unit"`
	if strings.Contains(bake, active) {
		t.Error("bake verifier must not require SSM to be active during imaging")
	}
	if !strings.Contains(runtime, active) {
		t.Error("runtime verifier must require SSM to be active")
	}
}
