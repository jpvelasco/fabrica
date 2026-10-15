package workstation

import "fmt"

// SessionPasswordParameter is the SecureString cloud-init reads at boot.
// The name is stable so a re-create overwrites a parameter left by a failed
// create, and terminate can delete it without a second record of the value.
// The operator source of truth remains .fabrica/workstation-credentials.yaml.
const SessionPasswordParameter = "/fabrica/workstation/dcv-password"

// SessionPasswordPolicyName is the inline policy that allows ssm:GetParameter
// on SessionPasswordParameter and nothing else.
const SessionPasswordPolicyName = "fabrica-workstation-dcv-password"

// SessionPasswordParameterARN is the IAM resource for SessionPasswordParameter.
// The name already starts with /, which is the separator in the SSM ARN
// (arn:aws:ssm:region:account:parameter/fabrica/...). Region and account are
// required so the role is not granted a wildcard.
func SessionPasswordParameterARN(region, account string) (string, error) {
	if region == "" || account == "" {
		return "", fmt.Errorf("region and account are required to scope %s", SessionPasswordParameter)
	}
	return fmt.Sprintf("arn:aws:ssm:%s:%s:parameter%s", region, account, SessionPasswordParameter), nil
}
