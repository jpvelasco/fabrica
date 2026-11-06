package horde

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// The Horde coordinator runs with built-in accounts, so `fabrica horde submit`
// and the `ci` CodeBuild project authenticate with a Horde service-account
// token. Cloud-init creates the service account on the instance (while Horde
// is still bound to loopback) and publishes its token to a per-create SSM
// SecureString. `horde create` pre-creates that parameter with a placeholder
// value and Fabrica's tags; the instance role may only overwrite exactly that
// parameter. No token is ever generated, stored, or logged on the operator
// side, and none passes through user data or Cloud Control desired state.

// ServiceTokenParameterPrefix is the hierarchical prefix of the per-create
// service-token SecureString.
const ServiceTokenParameterPrefix = "/fabrica/horde"

// serviceTokenNameSuffix is the trailing leaf of the per-create parameter name.
const serviceTokenNameSuffix = "/service-token"

// serviceTokenName is the exact per-create name shape: the prefix, a 32
// lowercase-hex nonce (NewServiceTokenNonce), and the leaf. Destroy, submit,
// and cloud-init validate recorded names against it so a hand-edited state
// entry can neither widen an IAM scope nor inject into the cloud-init script.
var serviceTokenName = regexp.MustCompile(`^/fabrica/horde/[0-9a-f]{32}/service-token$`)

// ServiceTokenPolicyName is the instance-role inline policy that allows
// ssm:PutParameter on exactly one service-token parameter ARN.
const ServiceTokenPolicyName = "fabrica-horde-service-token"

// ServiceTokenPlaceholder is the value `horde create` stores before cloud-init
// publishes the real token. Readers treat it as "not published yet".
const ServiceTokenPlaceholder = "pending-cloud-init"

// ServiceAccountName is the Horde service account cloud-init creates.
const ServiceAccountName = "fabrica-ci"

// ServiceAccountGroupClaimType and ServiceAccountGroup form the only claim the
// service account carries. The baked globals.json grants this claim job
// creation and viewing in its global ACL and nothing else, so the token is not
// a Horde administrator.
const (
	ServiceAccountGroupClaimType = "http://epicgames.com/ue/horde/group"
	ServiceAccountGroup          = "fabrica-ci"
)

// ServiceTokenAuthScheme is the HTTP Authorization scheme Horde's
// ServiceAccountAuthHandler accepts: "ServiceAccount <token>".
const ServiceTokenAuthScheme = "ServiceAccount"

// ServiceTokenParameterFor returns the per-create SecureString name for nonce.
func ServiceTokenParameterFor(nonce string) (string, error) {
	nonce = strings.TrimSpace(nonce)
	name := ServiceTokenParameterPrefix + "/" + nonce + serviceTokenNameSuffix
	if !serviceTokenName.MatchString(name) {
		return "", fmt.Errorf("the service-token parameter nonce must be 32 lowercase hex characters (got %q)", nonce)
	}
	return name, nil
}

// ServiceTokenParameterARN is the IAM resource for a per-create service-token
// parameter name. Region and account are required so no role is granted a
// wildcard.
func ServiceTokenParameterARN(region, account, name string) (string, error) {
	if region == "" || account == "" {
		return "", fmt.Errorf("region and account are required to scope %s", name)
	}
	if !IsServiceTokenName(name) {
		return "", fmt.Errorf("service-token parameter name %q is not a per-create %s name", name, ServiceTokenParameterPrefix)
	}
	return fmt.Sprintf("arn:aws:ssm:%s:%s:parameter%s", region, account, name), nil
}

// NewServiceTokenNonce returns a 16-byte random hex nonce that names a
// per-create service-token parameter.
func NewServiceTokenNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating service-token nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// IsServiceTokenName reports whether name is a well-formed per-create
// service-token parameter name.
func IsServiceTokenName(name string) bool {
	return serviceTokenName.MatchString(name)
}
