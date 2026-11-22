// Package secureparam names the per-create SSM SecureString a module uses to
// hand a secret to its instance without placing it in EC2 user data. Each
// create gets its own parameter so concurrent creates never share a secret and
// terminate deletes exactly the name it recorded.
package secureparam

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// nonceLen is the length of a valid nonce: 16 random bytes in lowercase hex.
const nonceLen = 32

// Scope is the family of parameter names one module owns. A name is
// Prefix + "/" + <32 lowercase-hex nonce> + Leaf, for example
// "/fabrica/perforce/<nonce>/admin-password".
type Scope struct {
	Prefix string // hierarchical module prefix, for example "/fabrica/perforce"
	Leaf   string // trailing segment, for example "/admin-password"
}

// NameFor returns the parameter name for one create nonce (a create UUID or an
// instance ID). A malformed nonce (empty, or containing a path separator) is
// rejected so it cannot widen the ARN scope.
func (s Scope) NameFor(nonce string) (string, error) {
	nonce = strings.TrimSpace(nonce)
	if nonce == "" || strings.ContainsAny(nonce, "/\\") {
		return "", fmt.Errorf("the %s parameter nonce must be a non-empty create UUID or instance ID without path separators (got %q)", s.Prefix, nonce)
	}
	return s.Prefix + "/" + nonce + s.Leaf, nil
}

// NewName mints a fresh per-create name in this scope.
func (s Scope) NewName() (string, error) {
	nonce, err := NewNonce()
	if err != nil {
		return "", err
	}
	return s.NameFor(nonce)
}

// IsName reports whether name is a well-formed per-create name in this scope.
// Terminate and drift validate a recorded name with it before they act on it,
// so a hand-edited or corrupted state entry cannot widen the target.
func (s Scope) IsName(name string) bool {
	rest, ok := strings.CutPrefix(name, s.Prefix+"/")
	if !ok {
		return false
	}
	nonce, ok := strings.CutSuffix(rest, s.Leaf)
	return ok && isNonce(nonce)
}

// ARN returns the IAM resource for a per-create name. The region and account
// are required so the role is never granted a wildcard, and the name is
// validated so the grant covers exactly one parameter.
func (s Scope) ARN(region, account, name string) (string, error) {
	if region == "" || account == "" {
		return "", fmt.Errorf("region and account are required to scope %s", name)
	}
	if !s.IsName(name) {
		return "", fmt.Errorf("parameter name %q is not a per-create %s/<nonce>%s name", name, s.Prefix, s.Leaf)
	}
	return fmt.Sprintf("arn:aws:ssm:%s:%s:parameter%s", region, account, name), nil
}

// NewNonce returns a 16-byte random lowercase-hex nonce.
func NewNonce() (string, error) {
	var b [nonceLen / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating secure-parameter nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func isNonce(s string) bool {
	if len(s) != nonceLen {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
