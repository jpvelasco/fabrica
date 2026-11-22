package workstation

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/secureparam"
)

// SessionPasswordParameterPrefix is the hierarchical prefix of the per-create
// DCV session-password SecureString. Each create names its own parameter under
// this prefix (see SessionPasswordParameterFor) so concurrent creates never
// write the same name, and terminate deletes exactly the name it recorded.
// The operator source of truth remains .fabrica/workstation-credentials.yaml.
const SessionPasswordParameterPrefix = "/fabrica/workstation"

// sessionPasswordScope is the name family for the DCV session password. The
// naming, ARN scoping, and nonce rules are shared with the other per-create
// SecureStrings in internal/secureparam.
var sessionPasswordScope = secureparam.Scope{Prefix: SessionPasswordParameterPrefix, Leaf: "/dcv-password"}

// SessionPasswordPolicyName is the inline policy that allows ssm:GetParameter
// on exactly one DCV session-password parameter ARN and nothing else.
const SessionPasswordPolicyName = "fabrica-workstation-dcv-password"

// SessionPasswordParameterFor returns the per-create SecureString name for the
// given nonce (a create UUID or an EC2 instance ID). A single shared name with
// Overwrite:true would let a concurrent create or a re-create clobber another
// instance's password, and a shared name in terminate would delete a different
// instance's secret. Per-create names keep each create's secret isolated and
// give terminate a precise target.
func SessionPasswordParameterFor(nonce string) (string, error) {
	return sessionPasswordScope.NameFor(nonce)
}

// SessionPasswordParameterARN is the IAM resource for a per-create
// session-password parameter name. Region and account are required so the role
// is not granted a wildcard; the name is scoped to the exact parameter so one
// instance cannot read another's.
func SessionPasswordParameterARN(region, account, name string) (string, error) {
	return sessionPasswordScope.ARN(region, account, name)
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

// IsSessionPasswordName reports whether name is a well-formed per-create
// session-password parameter name. Exported so the terminate flow validates
// the recorded name before deleting it: a hand-edited or corrupted state entry
// must not widen the delete target.
func IsSessionPasswordName(name string) bool {
	return sessionPasswordScope.IsName(name)
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
