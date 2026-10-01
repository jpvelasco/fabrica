package userdata

import (
	"strings"
	"testing"
)

func TestScrubShellFailsClosedOnlyWhenBothClearsFail(t *testing.T) {
	got := ScrubShell("configure", "admin password", ".fabrica/perforce-credentials.yaml")
	for _, want := range []string{
		"now that configure has consumed it",
		".fabrica/perforce-credentials.yaml",
		"http://169.254.169.254/latest/api/token",
		`-X PUT -H "X-aws-ec2-metadata-token: ${IMDS_TOKEN}" -d ""`,
		"ERROR: userdata scrub failed after configure; the admin password may still be reachable via IMDS user-data.",
		"truncate -s 0 /var/lib/cloud/instance/user-data.txt",
		"truncate -s 0 /var/lib/cloud/instance/user-data ",
		"Scrubbed EC2 userdata (local + IMDS).",
		`&& ! curl -s -X PUT -d ""`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ScrubShell missing %q", want)
		}
	}
	if strings.Contains(got, `|| curl -s -X PUT -d ""`) {
		t.Error("v1 fallback must be joined with '&& !', not '||'")
	}
	errIdx := strings.Index(got, "ERROR: userdata scrub failed")
	exitIdx := strings.Index(got, "exit 1")
	truncIdx := strings.Index(got, "truncate -s 0")
	if errIdx < 0 || exitIdx < errIdx || truncIdx < exitIdx {
		t.Error("failure path must echo ERROR, exit 1, and only then truncate on the success path")
	}
}

func TestScrubShellKeepsWorkstationWording(t *testing.T) {
	got := ScrubShell("chpasswd", "session password", ".fabrica/workstation-credentials.yaml")
	for _, want := range []string{
		"now that chpasswd has consumed it",
		".fabrica/workstation-credentials.yaml",
		"ERROR: userdata scrub failed after chpasswd; the session password may still be reachable via IMDS user-data.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("workstation wording missing %q", want)
		}
	}
}
