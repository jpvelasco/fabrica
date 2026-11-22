package userdata

import (
	"strings"
	"testing"
)

func TestAWSCLIInstallShellInstallsOnlyWhenMissing(t *testing.T) {
	got := AWSCLIInstallShell()
	for _, want := range []string{
		"command -v aws >/dev/null 2>&1 ||",
		"apt-get update -qq",
		"DEBIAN_FRONTEND=noninteractive apt-get install -y awscli",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AWSCLIInstallShell missing %q in %q", want, got)
		}
	}
	if !strings.HasSuffix(got, "\n") {
		t.Error("AWSCLIInstallShell must end with a newline so it can be concatenated")
	}
}

func TestSecureParameterFetchShellFailsClosed(t *testing.T) {
	got := SecureParameterFetchShell("ADMIN_PASS", "{{ .AdminPasswordParameter }}", "{{ .Region }}", "Helix Core admin password")
	for _, want := range []string{
		`if ! ADMIN_PASS=$(aws ssm get-parameter --name "{{ .AdminPasswordParameter }}" --with-decryption --query Parameter.Value --output text --region "{{ .Region }}"); then`,
		"ERROR: failed to read the Helix Core admin password from SSM parameter {{ .AdminPasswordParameter }}. Confirm the instance profile can ssm:GetParameter on that parameter.",
		`if [ -z "$ADMIN_PASS" ] || [ "$ADMIN_PASS" = "None" ]; then`,
		"ERROR: SSM parameter {{ .AdminPasswordParameter }} returned an empty Helix Core admin password.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("SecureParameterFetchShell missing %q", want)
		}
	}
	if n := strings.Count(got, "exit 1"); n != 2 {
		t.Errorf("SecureParameterFetchShell has %d exit 1 paths, want 2 (read failure and empty value)", n)
	}
}

// The fetch must never echo or write the value: the only place it may go is
// the variable assignment itself.
func TestSecureParameterFetchShellNeverEchoesValue(t *testing.T) {
	got := SecureParameterFetchShell("DCV_PASS", "{{ .P }}", "{{ .R }}", "DCV session password")
	for _, bad := range []string{"set -x", "echo \"$DCV_PASS", "echo $DCV_PASS", "> ", "tee "} {
		if strings.Contains(got, bad) {
			t.Errorf("SecureParameterFetchShell must not %q the secret", bad)
		}
	}
}
