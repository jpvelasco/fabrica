package workstation

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/secureparam"
)

// SessionPasswordScope is the name family for the DCV session password. Each
// create stores the password under its own SecureString
// (/<prefix>/workstation/<create-nonce>/dcv-password) so concurrent creates
// never write the same name, and terminate deletes exactly the name it
// recorded. The naming, ARN scoping, and nonce rules are shared with the other
// per-create SecureStrings in internal/secureparam. The operator source of
// truth remains .fabrica/workstation-credentials.yaml.
func SessionPasswordScope(p naming.Prefix) secureparam.Scope {
	return secureparam.Scope{Prefix: p.ParamPath("workstation"), Leaf: "/dcv-password"}
}

// SessionPasswordPolicyName is the inline policy that allows ssm:GetParameter
// on exactly one DCV session-password parameter ARN and nothing else.
func SessionPasswordPolicyName(p naming.Prefix) string {
	return p.For("workstation", "dcv", "password")
}

// NewSessionPasswordNonce returns a 16-byte random hex nonce to name a
// per-create session-password parameter. A nonce is used (rather than the
// instance ID) because cloud-init reads the parameter before the instance
// exists, so the name must be known at plan time. The nonce is recorded in
// state on the instance (Properties "sessionPasswordParameter") and in the
// IAM role ARN, so terminate can delete exactly the parameter it stored.
func NewSessionPasswordNonce() (string, error) {
	return secureparam.NewNonce()
}

// NewCreateNonce returns a 16-byte random hex nonce that names a create: the
// SDK launch path stamps it on the instance (CreateNonceTag) and the state
// layer records it (Properties "createNonce"). Each invocation mints a fresh
// nonce, so a terminate + re-create of the same instance name gets a fresh
// nonce and a fresh idempotency token; the nonce is per-invocation, and a
// re-run that finds an earlier run's instance (the SDK launch path's
// base-tag leftover check, ManagedBy + Name) fails with an actionable
// error instead of adopting the prior run's instance or launching a second
// one.
func NewCreateNonce() (string, error) {
	nonce, err := secureparam.NewNonce()
	if err != nil {
		return "", fmt.Errorf("generating create nonce: %w", err)
	}
	return nonce, nil
}
