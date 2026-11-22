package perforce

import (
	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/secureparam"
)

// AdminPasswordScope is the name family for the Helix Core admin password. Each
// create stores the password under its own SecureString
// (/<prefix>/perforce/<create-nonce>/admin-password) and the instance reads only
// that name at boot, so the password never rides EC2 user data (#700). The
// operator source of truth remains .fabrica/perforce-credentials.yaml.
func AdminPasswordScope(p naming.Prefix) secureparam.Scope {
	return secureparam.Scope{Prefix: p.ParamPath("perforce"), Leaf: "/admin-password"}
}

// AdminPasswordPolicyName is the inline policy that allows ssm:GetParameter on
// exactly one admin-password parameter ARN and nothing else.
func AdminPasswordPolicyName(p naming.Prefix) string {
	return p.For("perforce", "admin", "password")
}
