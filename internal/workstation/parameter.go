package workstation

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// SessionPasswordParameterPrefix is the hierarchical prefix of the per-create
// DCV session-password SecureString. Each create names its own parameter under
// this prefix (see SessionPasswordParameterFor) so concurrent creates never
// write the same name, and terminate deletes exactly the name it recorded.
// The operator source of truth remains .fabrica/workstation-credentials.yaml.
const SessionPasswordParameterPrefix = "/fabrica/workstation"

// SessionPasswordPolicyName is the inline policy that allows ssm:GetParameter
// on exactly one DCV session-password parameter ARN and nothing else.
const SessionPasswordPolicyName = "fabrica-workstation-dcv-password"

// sessionPasswordNameSuffix is the trailing leaf of the per-create parameter name.
const sessionPasswordNameSuffix = "/dcv-password"

// SessionPasswordParameterFor returns the per-create SecureString name for the
// given nonce (a create UUID or an EC2 instance ID). A single shared name with
// Overwrite:true would let a concurrent create or a re-create clobber another
// instance's password, and a shared name in terminate would delete a different
// instance's secret. Per-create names keep each create's secret isolated and
// give terminate a precise target. The name is validated so a malformed nonce
// (empty, or containing a path separator) cannot widen the ARN scope.
func SessionPasswordParameterFor(nonce string) (string, error) {
	nonce = strings.TrimSpace(nonce)
	if nonce == "" || strings.ContainsAny(nonce, "/\\") {
		return "", fmt.Errorf("the session-password parameter nonce must be a non-empty create UUID or instance ID without path separators (got %q)", nonce)
	}
	return SessionPasswordParameterPrefix + "/" + nonce + sessionPasswordNameSuffix, nil
}

// SessionPasswordParameterARN is the IAM resource for a per-create
// session-password parameter name. The name already starts with /, which is the
// separator in the SSM ARN (arn:aws:ssm:region:account:parameter/fabrica/...).
// Region and account are required so the role is not granted a wildcard; the
// name is scoped to the exact parameter so one instance cannot read another's.
func SessionPasswordParameterARN(region, account, name string) (string, error) {
	if region == "" || account == "" {
		return "", fmt.Errorf("region and account are required to scope %s", name)
	}
	if !isSessionPasswordName(name) {
		return "", fmt.Errorf("session-password parameter name %q is not a per-create %s name", name, SessionPasswordParameterPrefix)
	}
	return fmt.Sprintf("arn:aws:ssm:%s:%s:parameter%s", region, account, name), nil
}

// NewSessionPasswordNonce returns a 16-byte random hex nonce to name a
// per-create session-password parameter. A nonce is used (rather than the
// instance ID) because cloud-init reads the parameter before the instance
// exists, so the name must be known at plan time. The nonce is recorded in
// state on the instance (Properties "sessionPasswordParameter") and in the
// IAM role ARN, so terminate can delete exactly the parameter it stored.
func NewSessionPasswordNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating session-password nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// IsSessionPasswordName reports whether name is a well-formed per-create
// session-password parameter name. Exported so the terminate flow validates
// the recorded name before deleting it: a hand-edited or corrupted state entry
// must not widen the delete target.
func IsSessionPasswordName(name string) bool {
	return isSessionPasswordName(name)
}

// isSessionPasswordName reports whether name is a well-formed per-create
// session-password parameter name.
func isSessionPasswordName(name string) bool {
	const delim = "/"
	if len(name) < len(SessionPasswordParameterPrefix)+len(delim)+len(sessionPasswordNameSuffix) {
		return false
	}
	if !strings.HasPrefix(name, SessionPasswordParameterPrefix+delim) {
		return false
	}
	if !strings.HasSuffix(name, sessionPasswordNameSuffix) {
		return false
	}
	mid := name[len(SessionPasswordParameterPrefix+delim) : len(name)-len(sessionPasswordNameSuffix)]
	return mid != "" && !strings.ContainsAny(mid, "/\\")
}
