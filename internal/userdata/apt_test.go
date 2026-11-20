package userdata

import (
	"strings"
	"testing"
)

func TestPerforceAptRepoShellExportsOnlyThePinnedKey(t *testing.T) {
	got := PerforceAptRepoShell()
	for _, want := range []string{
		"install -m 0755 -d /etc/apt/keyrings",
		`wget -q https://package.perforce.com/perforce.pubkey -O "$p4_gnupg/perforce.pubkey"`,
		`GNUPGHOME="$p4_gnupg" gpg --batch --quiet --import`,
		`GNUPGHOME="$p4_gnupg" gpg --batch --export E58131C0AEA7B082C6DC4C937123CB760FF18869 > /etc/apt/keyrings/perforce.gpg`,
		"chmod a+r /etc/apt/keyrings/perforce.gpg",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("PerforceAptRepoShell missing %q", want)
		}
	}
	// A grep for the pinned fpr passes a key file that also carries other
	// keys; exporting by fingerprint is what keeps them out of the keyring.
	if strings.Contains(got, "grep -x 'fpr") {
		t.Error("PerforceAptRepoShell must export the pinned key, not grep for its fingerprint")
	}
}

func TestPerforceAptRepoShellFailsClosedWithoutThePinnedKey(t *testing.T) {
	got := PerforceAptRepoShell()
	exportIdx := strings.Index(got, "gpg --batch --export")
	checkIdx := strings.Index(got, "test -s /etc/apt/keyrings/perforce.gpg || {")
	exitIdx := strings.Index(got, "exit 1; }")
	sourceIdx := strings.Index(got, "sources.list.d")
	if exportIdx < 0 || checkIdx < exportIdx || exitIdx < checkIdx || sourceIdx < exitIdx {
		t.Error("an empty export must exit 1 before any source list is written")
	}
}

func TestPerforceAptRepoShellSignedByAndHTTPS(t *testing.T) {
	got := PerforceAptRepoShell()
	want := "deb [signed-by=/etc/apt/keyrings/perforce.gpg] https://package.perforce.com/apt/ubuntu ${VERSION_CODENAME} release"
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
