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
