package amissm

import (
	"regexp"
	"strings"
	"testing"
)

// imageBuilderNamePattern is the EC2 Image Builder name pattern for component
// and image-recipe names. The sanitizer must always produce a match.
var imageBuilderNamePattern = regexp.MustCompile(`^[A-Za-z0-9][-_A-Za-z0-9]{0,126}[A-Za-z0-9]$`)

func TestSanitizeImageBuilderName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// The #463 case: default names embed the dotted version.
		{in: "fabrica-horde-5.8.0-docker", want: "fabrica-horde-5-8-0-docker"},
		{in: "fabrica-horde-5.8.0", want: "fabrica-horde-5-8-0"},
		{in: "fabrica-lore-5.5.0", want: "fabrica-lore-5-5-0"},
		{in: "fabrica-horde-latest", want: "fabrica-horde-latest"},
		// Already-valid names pass through unchanged.
		{in: "fabrica-horde-5-8-0", want: "fabrica-horde-5-8-0"},
		{in: "My-Recipe_Name1", want: "My-Recipe_Name1"},
		{in: "a1", want: "a1"},
		// Single characters are illegal (needs 2+); extend with a letter.
		{in: "a", want: "af"},
		{in: "-", want: "fabrica-ami"},
		// Dots become dashes; other illegal characters are dropped.
		{in: "a.b.c", want: "a-b-c"},
		{in: "bad name!", want: "badname"},
		{in: "v1.0.0", want: "v1-0-0"},
		// Edge dashes/underscores are trimmed; empty results fall back.
		{in: "-leading", want: "leading"},
		{in: "trailing-", want: "trailing"},
		{in: "___", want: "fabrica-ami"},
		{in: "", want: "fabrica-ami"},
		{in: "!!!", want: "fabrica-ami"},
		// Long names are capped at 128 without breaking the pattern.
		{in: strings.Repeat("a", 200), want: strings.Repeat("a", 128)},
		{in: strings.Repeat("a", 127) + "-", want: strings.Repeat("a", 127)},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := SanitizeImageBuilderName(tc.in)
			if got != tc.want {
				t.Fatalf("SanitizeImageBuilderName(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !imageBuilderNamePattern.MatchString(got) {
				t.Fatalf("SanitizeImageBuilderName(%q) = %q does not match the Image Builder pattern", tc.in, got)
			}
		})
	}
}

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
