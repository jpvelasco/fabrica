package perforce

import "github.com/jpvelasco/fabrica/internal/secureparam"

// AdminPasswordScope is the name family for the Helix Core admin password. Each
// create stores the password under its own SecureString
// (/fabrica/perforce/<create-nonce>/admin-password) and the instance reads only
// that name at boot, so the password never rides EC2 user data (#700). The
// operator source of truth remains .fabrica/perforce-credentials.yaml.
var AdminPasswordScope = secureparam.Scope{Prefix: "/fabrica/perforce", Leaf: "/admin-password"}

// AdminPasswordPolicyName is the inline policy that allows ssm:GetParameter on
// exactly one admin-password parameter ARN and nothing else.
const AdminPasswordPolicyName = "fabrica-perforce-admin-password"
