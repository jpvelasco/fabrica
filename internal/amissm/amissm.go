// Package amissm generates the fail-closed Amazon SSM Agent bake snippets
// and the Image Builder name sanitizer shared by Fabrica AMI contracts
// (Lore, Horde, and later DDC/workstation).
package amissm

import (
	"fmt"
	"strings"
)

// Ubuntu 22.04 ships Amazon SSM Agent as either the deb unit or the snap unit
// depending on the base AMI publisher. Both are acceptable; neither may be
// skipped. A bake that cannot enable one of these units must fail closed.
const (
	DebUnit        = "amazon-ssm-agent.service"
	SnapUnit       = "snap.amazon-ssm-agent.amazon-ssm-agent.service"
	enabledStates  = "enabled|enabled-runtime|alias|indirect"
	disabledStates = "disabled|disabled-runtime"
)

// EnsureScript installs (if needed) and enables Amazon SSM Agent. It is
// intended for AMI install/bake scripts and aborts if the unit cannot be
// enabled. A static unit is accepted only for the snap agent.
func EnsureScript() string {
	return `# Amazon SSM Agent is required for private-subnet management.
# Fail closed: a missing or non-enableable unit aborts the bake.
` + detectUnitBash() + `if [ -z "$ssm_unit" ]; then
  snap install amazon-ssm-agent --classic
` + detectUnitBash() + `fi
if [ -z "$ssm_unit" ]; then
  echo "amazon-ssm-agent is not installed (neither ` + DebUnit + ` nor ` + SnapUnit + ` is present after install). Use an Ubuntu 22.04 base AMI that includes the SSM agent, or install it before this step." >&2
  exit 1
fi
` + unitStateSwitchBash(true)
}

// RequireEnabledScript asserts that Amazon SSM Agent is installed and
// enableable. When mustBeActive is true (runtime, after boot) it also requires
// the unit to be active. Bake verification must pass false: imaging may stop
// services before snapshot.
func RequireEnabledScript(mustBeActive bool) string {
	script := detectUnitBash() + `if [ -z "$ssm_unit" ]; then
  echo "amazon-ssm-agent is not installed (neither ` + DebUnit + ` nor ` + SnapUnit + `)" >&2
  exit 1
fi
` + unitStateSwitchBash(false)
	if mustBeActive {
		script += `systemctl is-active --quiet "$ssm_unit"
`
	}
	return script
}

func detectUnitBash() string {
	return fmt.Sprintf(`ssm_unit=""
if systemctl cat %[1]s >/dev/null 2>&1; then
  ssm_unit=%[1]s
elif systemctl cat %[2]s >/dev/null 2>&1; then
  ssm_unit=%[2]s
fi
`, DebUnit, SnapUnit)
}

func unitStateSwitchBash(enableIfDisabled bool) string {
	disabledBranch := ""
	if enableIfDisabled {
		disabledBranch = "  " + disabledStates + ")\n    systemctl enable \"$ssm_unit\"\n    ;;\n"
	}
	fail := "amazon-ssm-agent unit $ssm_unit is not enabled (state=$unit_state)"
	if enableIfDisabled {
		fail = "amazon-ssm-agent unit $ssm_unit cannot be enabled (state=$unit_state). Install and enable the agent, then retry the bake."
	}
	return `unit_state=$(systemctl is-enabled "$ssm_unit" 2>/dev/null || :)
case "$unit_state" in
  ` + enabledStates + `) ;;
  static)
    if [ "$ssm_unit" != "` + SnapUnit + `" ]; then
      echo "amazon-ssm-agent unit $ssm_unit is static and will not start at boot" >&2
      exit 1
    fi
    ;;
` + disabledBranch + `  *)
    echo "` + fail + `" >&2
    exit 1
    ;;
esac
`
}

// SanitizeImageBuilderName rewrites a component or image-recipe name so it
// always matches EC2 Image Builder's name pattern (2-128 characters, first
// and last alphanumeric, only letters, numbers, underscores, and hyphens
// inside). Dots are legal in EC2 resource names but illegal here, so
// version-bearing default names like "fabrica-horde-5.8.0" must be sanitized
// before they reach create-component or create-image-recipe. Dots become
// dashes so a version reads the same way (5.8.0 → 5-8-0); every other
// rejected character is dropped, and edge dashes are trimmed.
func SanitizeImageBuilderName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '.':
			b.WriteByte('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > 128 {
		s = s[:128]
	}
	// Trim after the length cap: a truncation point can leave a leading or
	// trailing dash/underscore that the pattern rejects.
	s = strings.Trim(s, "-_")
	if s == "" {
		return "fabrica-ami"
	}
	if len(s) == 1 {
		s += "f"
	}
	return s
}
