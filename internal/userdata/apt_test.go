package userdata

import (
	"strings"
	"testing"
)

func TestPerforceAptRepoShellPinsFingerprintInKeyring(t *testing.T) {
	got := PerforceAptRepoShell()
	for _, want := range []string{
		"install -m 0755 -d /etc/apt/keyrings",
		"wget -q https://package.perforce.com/perforce.pubkey -O /etc/apt/keyrings/perforce.asc",
		"grep -x 'fpr:::::::::E58131C0AEA7B082C6DC4C937123CB760FF18869:'",
		"ERROR: could not verify the Perforce apt key",
		"chmod a+r /etc/apt/keyrings/perforce.asc",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("PerforceAptRepoShell missing %q", want)
		}
	}
}

func TestPerforceAptRepoShellFailsClosedOnFingerprintMismatch(t *testing.T) {
	got := PerforceAptRepoShell()
	checkIdx := strings.Index(got, "grep -x")
	failIdx := strings.Index(got, "|| {")
	exitIdx := strings.Index(got, "exit 1; }")
	chmodIdx := strings.Index(got, "chmod a+r")
	if checkIdx < 0 || failIdx < checkIdx || exitIdx < failIdx || chmodIdx < exitIdx {
		t.Error("the fingerprint check must exit 1 before the key is made readable or any source list is written")
	}
}

func TestPerforceAptRepoShellSignedByAndHTTPS(t *testing.T) {
	got := PerforceAptRepoShell()
	want := "deb [signed-by=/etc/apt/keyrings/perforce.asc] https://package.perforce.com/apt/ubuntu ${VERSION_CODENAME} release"
	if !strings.Contains(got, want) {
		t.Errorf("PerforceAptRepoShell missing repo line %q", want)
	}
	if strings.Contains(got, "http://") {
		t.Error("Perforce apt fragment must not use plain http")
	}
}

func TestPerforceAptRepoShellUsesOSCodename(t *testing.T) {
	got := PerforceAptRepoShell()
	if !strings.Contains(got, ". /etc/os-release") {
		t.Error("codename must come from /etc/os-release")
	}
	for _, banned := range []string{"jammy", "focal"} {
		if strings.Contains(got, banned) {
			t.Errorf("PerforceAptRepoShell must not hard-code the release %q", banned)
		}
	}
}

func TestPerforceAptRepoShellRefreshesIndexAndAvoidsAptKey(t *testing.T) {
	got := PerforceAptRepoShell()
	if strings.Contains(got, "apt-key") {
		t.Error("PerforceAptRepoShell must not use the deprecated global apt-key keyring")
	}
	if strings.Contains(got, "add-apt-repository") {
		t.Error("PerforceAptRepoShell must not use add-apt-repository")
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "apt-get update") {
		t.Error("PerforceAptRepoShell must end by refreshing the apt index")
	}
}

func TestPerforceAptRepoShellHasNoTemplateActions(t *testing.T) {
	if strings.Contains(PerforceAptRepoShell(), "{{") {
		t.Error("fragment is concatenated into a text/template; it must not contain template actions")
	}
}
