package aws

import (
	"fmt"

	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

// ssoLoginHint is appended when AWS credentials look like an expired SSO session.
const ssoLoginHint = "SSO session expired or invalid — run 'aws sso login' for the configured profile (cloud.aws.profile or AWS_PROFILE) and retry"

func wrapAWSAuthError(op string, profile string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if !fabricac.IsExpiredSSOSession(msg) {
		return fmt.Errorf("%s: %w", op, err)
	}
	if profile != "" {
		return fmt.Errorf("%s: %w — %s (profile %q)", op, err, ssoLoginHint, profile)
	}
	return fmt.Errorf("%s: %w — %s", op, err, ssoLoginHint)
}
