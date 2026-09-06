package cloud

import "strings"

// IsExpiredSSOSession reports whether an AWS error string looks like an
// expired or unrefreshable SSO session.
func IsExpiredSSOSession(msg string) bool {
	lower := strings.ToLower(msg)
	for _, needle := range []string{
		"sso session",
		"token has expired and refresh failed",
		"failed to refresh cached credentials",
		"the sso session associated with this profile has expired",
		"expiredtoken",
		"invalidgrantexception",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
