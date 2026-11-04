package tags

// ManagedByKey and ManagedByValue are the identity tag pair that marks a
// resource as Fabrica-owned. Teardown, drift, and cost attribution key off
// this tag.
const (
	ManagedByKey   = "ManagedBy"
	ManagedByValue = "fabrica"
)

// Standard returns the default Fabrica resource tags for a given module and version.
// This is the single source of truth for all Fabrica-managed resource tagging.
func Standard(module, version string) map[string]string {
	return map[string]string{
		ManagedByKey:     ManagedByValue,
		"FabricaModule":  module,
		"FabricaVersion": version,
	}
}

// Merge returns the standard Fabrica tags for module/version with extra
// operator tags layered on top. An operator tag overrides the standard tag
// with the same key (for example ManagedBy).
func Merge(module, version string, extra map[string]string) map[string]string {
	tags := Standard(module, version)
	for k, v := range extra {
		tags[k] = v
	}
	return tags
}

// identityTagKeys are the Fabrica-owned identity tags. Teardown, drift, and
// cost attribution key off them, so operator tags (cloud.aws.tags) may add
// to the tag set but never shadow these — a resource that lost ManagedBy
// would leak on `destroy --all` and skew drift and cost reporting.
var identityTagKeys = map[string]struct{}{
	ManagedByKey:         {},
	"FabricaModule":      {},
	"FabricaVersion":     {},
	"FabricaCreateNonce": {},
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
// operator tags, which add keys but never override an existing key. Operator
// tags are additive; identity tags are Fabrica-owned. Empty keys are
// dropped: the tag APIs reject them.
func MergeProtected(module, version string, base, extra map[string]string) map[string]string {
	out := make(map[string]string)
	for k, v := range base {
		out[k] = v
	}
	out["ManagedBy"] = "fabrica"
	out["FabricaVersion"] = version
	if _, ok := out["FabricaModule"]; !ok {
		out["FabricaModule"] = module
	}
	for k, v := range extra {
		if k == "" || IdentityKey(k) {
			continue
		}
		out[k] = v
	}
	return out
}
