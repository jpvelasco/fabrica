package horde

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/jpvelasco/fabrica/internal/naming"
)

// The Horde coordinator runs with built-in accounts, so `fabrica horde submit`
// and the `ci` CodeBuild project authenticate with a Horde service-account
// token. Cloud-init creates the service account on the instance (while Horde
// is still bound to loopback) and publishes its token to a per-create SSM
// SecureString. `horde create` pre-creates that parameter with a placeholder
// value and Fabrica's tags; the instance role may only overwrite exactly that
// parameter. No token is ever generated, stored, or logged on the operator
// side, and none passes through user data or Cloud Control desired state.

// serviceTokenNameSuffix is the trailing leaf of the per-create parameter name.
const serviceTokenNameSuffix = "/service-token"

// serviceTokenName is the exact per-create name shape: /<namePrefix>/horde, a
// 32 lowercase-hex nonce (NewServiceTokenNonce), and the leaf. The prefix is
// validated by internal/naming when it is minted, so this pattern only needs to
// accept any valid prefix. Destroy, submit, the ci module, and cloud-init check
// recorded names against it so a hand-edited state entry can neither widen an
// IAM scope nor inject into the cloud-init script.
var serviceTokenName = regexp.MustCompile(`^/[a-z][a-z0-9-]{0,19}/horde/[0-9a-f]{32}/service-token$`)

// ServiceTokenPolicyName is the instance-role inline policy that allows
// ssm:PutParameter on exactly one service-token parameter ARN.
func ServiceTokenPolicyName(p naming.Prefix) string {
	return p.For("horde", "service", "token")
}

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

// ServiceTokenParameterFor returns the per-create SecureString name for nonce,
// under the module path of prefix (/<prefix>/horde/<nonce>/service-token).
func ServiceTokenParameterFor(prefix naming.Prefix, nonce string) (string, error) {
	nonce = strings.TrimSpace(nonce)
	name := prefix.ParamPath("horde") + "/" + nonce + serviceTokenNameSuffix
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
		return "", fmt.Errorf("service-token parameter name %q is not a per-create /<namePrefix>/horde/<nonce>/service-token name", name)
	}
	return fmt.Sprintf("arn:aws:ssm:%s:%s:parameter%s", region, account, name), nil
}

// NewServiceTokenNonce returns a 16-byte random hex nonce that names a
// per-create service-token parameter.
func NewServiceTokenNonce() (string, error) {
	return readServiceTokenNonce(rand.Reader)
}

func readServiceTokenNonce(r io.Reader) (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("generating service-token nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// IsServiceTokenName reports whether name is a well-formed per-create
// service-token parameter name.
func IsServiceTokenName(name string) bool {
	return serviceTokenName.MatchString(name)
}
