package userdata

import "fmt"

// AWSCLIInstallShell returns the fragment that installs the AWS CLI when the
// image lacks it. Stock Ubuntu images and SSM sessions do not ship awscli; the
// instance profile supplies the credentials. The apt package is the v1 CLI,
// which supports ssm get-parameter --with-decryption.
func AWSCLIInstallShell() string {
	return "command -v aws >/dev/null 2>&1 || { apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y awscli; }\n"
}

// SecureParameterFetchShell returns the cloud-init fragment that reads one SSM
// SecureString into the shell variable varName with
// aws ssm get-parameter --with-decryption. nameRef and regionRef are the text
// that renders the parameter name and region (for example "{{ .Region }}"),
// and subject is the credential noun used in the error lines.
//
// The value lives only in the variable. The fragment never echoes it, writes
// it to a file, or logs it, and the caller must feed it to a heredoc or pipe.
// It fails closed: a read error or an empty value exits 1.
func SecureParameterFetchShell(varName, nameRef, regionRef, subject string) string {
	return fmt.Sprintf(`if ! %[1]s=$(aws ssm get-parameter --name "%[2]s" --with-decryption --query Parameter.Value --output text --region "%[3]s"); then
  echo "ERROR: failed to read the %[4]s from SSM parameter %[2]s. Confirm the instance profile can ssm:GetParameter on that parameter."
  exit 1
fi
if [ -z "$%[1]s" ] || [ "$%[1]s" = "None" ]; then
  echo "ERROR: SSM parameter %[2]s returned an empty %[4]s."
  exit 1
fi
`, varName, nameRef, regionRef, subject)
}
