package userdata

import (
	"strings"
	"testing"
)

func TestScrubShellFailsClosedWhenLocalCopySurvives(t *testing.T) {
	got := ScrubShell("configure", "admin password", ".fabrica/perforce-credentials.yaml")
	for _, want := range []string{
		"now that configure has consumed the password",
		".fabrica/perforce-credentials.yaml",
		"truncate -s 0 /var/lib/cloud/instance/user-data.txt",
		"truncate -s 0 /var/lib/cloud/instance/user-data.txt.i",
		"truncate -s 0 /var/lib/cloud/instance/user-data\n",
		"ERROR: userdata scrub failed after configure; a local copy of the user data still holds the admin password.",
		"Scrubbed EC2 userdata (local).",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ScrubShell missing %q", want)
		}
	}
	// The scrub is local-only: IMDS /latest/user-data is read-only to the
	// instance (a PUT there is a no-op), so the fragment must not contact the
	// metadata service at all (#489).
	for _, gone := range []string{"169.254.169.254", "curl", "IMDS_TOKEN", "/latest/api/token"} {
		if strings.Contains(got, gone) {
			t.Errorf("scrub must not contact IMDS; found %q", gone)
		}
	}
	// Fail closed: the empty-check exits 1 after the truncates and before the
	// success line.
	truncIdx := strings.Index(got, "truncate -s 0")
	checkIdx := strings.Index(got, "test -s /var/lib/cloud/instance/user-data")
	errIdx := strings.Index(got, "ERROR: userdata scrub failed")
	exitIdx := strings.Index(got, "exit 1")
	okIdx := strings.Index(got, "Scrubbed EC2 userdata (local).")
	if truncIdx < 0 || checkIdx < truncIdx || errIdx < checkIdx || exitIdx < errIdx || okIdx < exitIdx {
		t.Error("failure path must truncate, re-verify empty, echo ERROR, exit 1, and only then log success")
	}
}

func TestScrubShellKeepsWorkstationWording(t *testing.T) {
	got := ScrubShell("chpasswd", "session password", ".fabrica/workstation-credentials.yaml")
	for _, want := range []string{
		"now that chpasswd has consumed the password",
		".fabrica/workstation-credentials.yaml",
		"ERROR: userdata scrub failed after chpasswd; a local copy of the user data still holds the session password.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("workstation wording missing %q", want)
		}
	}
}
