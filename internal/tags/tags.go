package tags

// Standard returns the default Fabrica resource tags for a given module and version.
// This is the single source of truth for all Fabrica-managed resource tagging.
func Standard(module, version string) map[string]string {
	return map[string]string{
		"ManagedBy":      "fabrica",
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
