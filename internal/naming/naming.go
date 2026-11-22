// Package naming owns the physical AWS resource names Fabrica generates. It is
// pure: no AWS SDK, config, or module imports, so any package can depend on it.
package naming

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultName is the prefix used when no custom name prefix is configured.
const DefaultName = "fabrica"

// MaxLen is the longest accepted prefix. The S3 bucket
// <prefix>-lore-store-<12-digit account>-<region> is the binding limit: with
// the longest region code (14 characters) it allows a 24-character prefix under
// S3's 63-character cap. MaxLen keeps 4 characters of headroom below that.
const MaxLen = 20

// Prefix is an immutable resource-name prefix. Its zero value is the default
// ("fabrica"); custom values can only be constructed through Resolve.
type Prefix struct {
	value string
}

// Default is the zero-value prefix. Every name built from it is byte-identical
// to the names Fabrica has always generated.
var Default Prefix

var prefixPattern = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,18}[a-z0-9])?$`)

// Resolve validates a configured prefix. An empty or "fabrica" value selects
// the default.
func Resolve(s string) (Prefix, error) {
	if s == "" || s == DefaultName {
		return Default, nil
	}
	if err := validate(s); err != nil {
		return Prefix{}, err
	}
	return Prefix{value: s}, nil
}

// MustResolve is Resolve for constant prefixes; it panics on an invalid value.
func MustResolve(s string) Prefix {
	p, err := Resolve(s)
	if err != nil {
		panic(err)
	}
	return p
}

// String returns the prefix text, "fabrica" for the default.
func (p Prefix) String() string {
	if p.value == "" {
		return DefaultName
	}
	return p.value
}

func validate(s string) error {
	if !prefixPattern.MatchString(s) {
		return fmt.Errorf("namePrefix %q is not valid: use 1-%d lowercase letters, digits, and hyphens, starting with a letter and ending with a letter or digit (for example \"acme\")", s, MaxLen)
	}
	if strings.Contains(s, "--") {
		return fmt.Errorf("namePrefix %q contains \"--\": use single hyphens between words (for example \"acme-lab\")", s)
	}
	if isReserved(s) {
		return fmt.Errorf("namePrefix %q is reserved by AWS naming rules (sg, sg-*, xn-*, sthree-*): choose a different prefix", s)
	}
	return nil
}

func isReserved(s string) bool {
	return s == "sg" || strings.HasPrefix(s, "sg-") || strings.HasPrefix(s, "xn-") || strings.HasPrefix(s, "sthree-")
}

// For returns "<prefix>-<module>" followed by "-<kind>" for each kind.
func (p Prefix) For(module string, kinds ...string) string {
	parts := append([]string{p.String(), module}, kinds...)
	return strings.Join(parts, "-")
}

// SG returns the security group name for a module.
func (p Prefix) SG(module string) string { return p.For(module, "sg") }

// Role returns the IAM role name for a module.
func (p Prefix) Role(module string) string { return p.For(module, "role") }

// Profile returns the instance profile name for a module.
func (p Prefix) Profile(module string) string { return p.For(module, "profile") }

// Instance returns the Name tag and default instance name for a module.
func (p Prefix) Instance(module string) string { return p.For(module) }

// Bucket returns "<prefix>-<base>-<account>-<region>".
func (p Prefix) Bucket(base, account, region string) string {
	return p.For(base, account, region)
}

// ParamPath returns the SSM parameter path root for a module.
func (p Prefix) ParamPath(module string) string { return p.slashPath(module) }

// LogGroup returns the CloudWatch Logs group name for a module.
func (p Prefix) LogGroup(module string) string { return p.slashPath(module) }

func (p Prefix) slashPath(module string) string {
	return "/" + p.String() + "/" + module
}
