package tags

import (
	"fmt"
	"regexp"
)

// Identity is an immutable resource owner. Its zero value selects Fabrica's
// default owner; custom values can only be constructed through ResolveIdentity.
type Identity struct {
	managedBy string
}

var managedByPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// ResolveIdentity validates an owner without trimming or changing its case.
// An omitted or empty owner selects the default.
func ResolveIdentity(managedBy string) (Identity, error) {
	if managedBy == "" {
		return Identity{}, nil
	}
	if !managedByPattern.MatchString(managedBy) {
		return Identity{}, fmt.Errorf("managedBy %q must match [A-Za-z0-9][A-Za-z0-9._-]{0,62}; choose an owner of 1–63 characters without whitespace", managedBy)
	}
	return Identity{managedBy: managedBy}, nil
}

// ManagedBy returns the resolved, nonempty resource owner.
func (i Identity) ManagedBy() string {
	if i.managedBy == "" {
		return ManagedByValue
	}
	return i.managedBy
}

// Standard returns this owner's standard module and version tags.
func (i Identity) Standard(module, version string) map[string]string {
	return map[string]string{
		ManagedByKey:     i.ManagedBy(),
		"FabricaModule":  module,
		"FabricaVersion": version,
	}
}

// MergeProtected copies trusted base tags, forces this owner, and merges
// ordinary operator tags with their existing precedence. Operator tags cannot
// supply reserved keys, including reserved keys absent from base.
func (i Identity) MergeProtected(base, extra map[string]string) map[string]string {
	out := make(map[string]string)
	for k, v := range base {
		out[k] = v
	}
	out[ManagedByKey] = i.ManagedBy()
	for k, v := range extra {
		if k == "" || IdentityKey(k) {
			continue
		}
		out[k] = v
	}
	return out
}
