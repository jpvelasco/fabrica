package cloud

import "testing"

func TestIsExpiredSSOSession(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"the SSO session associated with this profile has expired or is otherwise invalid", true},
		{"failed to refresh cached credentials, the SSO session has expired", true},
		{"Token has expired and refresh failed", true},
		{"ExpiredToken: The security token included in the request is expired", true},
		{"access denied", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsExpiredSSOSession(tt.msg); got != tt.want {
			t.Errorf("IsExpiredSSOSession(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}
