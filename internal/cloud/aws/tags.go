package aws

import (
	"encoding/json"
)

// injectFabricaTags merges standard Fabrica tags into the desired state of a
// resource. The existing tags in the desired state are preserved; new tags are
// added or override existing values.
func injectFabricaTags(state json.RawMessage, module, version string, extra map[string]string) json.RawMessage {
	if len(state) == 0 {
		state = json.RawMessage(`{}`)
	}

	var m map[string]any
	if err := json.Unmarshal(state, &m); err != nil {
		// If we can't parse the desired state as JSON, return it unchanged.
		return state
	}

	tags, ok := m["tags"].(map[string]any)
	if !ok {
		tags = make(map[string]any)
	}

	tags["ManagedBy"] = "fabrica"
	tags["FabricaModule"] = module
	tags["FabricaVersion"] = version
	for k, v := range extra {
		tags[k] = v
	}

	m["tags"] = tags
	out, err := json.Marshal(m)
	if err != nil {
		return state
	}

	return out
}
