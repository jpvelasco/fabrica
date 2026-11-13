package tags

// ManagedByKey identifies resource ownership. ManagedByValue is the immutable
// default owner, not a configurable package-level setting.
const (
	ManagedByKey        = "ManagedBy"
	ManagedByValue      = "fabrica"
	OwnershipVersionKey = "FabricaOwnershipVersion"
)

// Standard returns the default Fabrica resource tags for a given module and version.
// This is the single source of truth for all Fabrica-managed resource tagging.
func Standard(module, version string) map[string]string {
	return (Identity{}).Standard(module, version)
}

// Merge returns the standard Fabrica tags for module/version with extra
// operator tags layered on top. An operator tag overrides the standard tag
// with the same key (for example ManagedBy).
//
// Retained for setup compatibility until backend provenance is validated.
// Other resource paths must use protected merging.
func Merge(module, version string, extra map[string]string) map[string]string {
	tags := Standard(module, version)
	for k, v := range extra {
		tags[k] = v
	}
	return tags
}

// identityTagKeys are reserved for trusted resource metadata. Operator tags
// may add ordinary metadata but cannot select or forge these values.
var identityTagKeys = map[string]struct{}{
	ManagedByKey:         {},
	"FabricaModule":      {},
	"FabricaVersion":     {},
	"FabricaCreateNonce": {},
	OwnershipVersionKey:  {},
}

// IdentityKey reports whether key is a Fabrica-owned identity tag that
// operator tags must not override.
func IdentityKey(key string) bool {
	_, ok := identityTagKeys[key]
	return ok
}

// MergeProtected returns the tag set for a resource: base (existing
// desired-state tags, if any) with the standard Fabrica tags for module and
// version layered on top — the standard set forces ManagedBy and
// FabricaVersion and keeps base's FabricaModule when present — then extra
// operator tags, which retain their precedence for ordinary keys. Identity
// tags are Fabrica-owned. Empty operator keys are dropped: tag APIs reject them.
func MergeProtected(module, version string, base, extra map[string]string) map[string]string {
	out := (Identity{}).MergeProtected(base, extra)
	out["FabricaVersion"] = version
	if _, ok := out["FabricaModule"]; !ok {
		out["FabricaModule"] = module
	}
	return out
}
