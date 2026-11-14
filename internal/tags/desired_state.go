package tags

import "encoding/json"

// RebindDesiredState replaces owner values in existing tag arrays, including
// launch-template specifications and ASG propagation tags, preserving their
// schema and metadata. Malformed documents remain unchanged for the caller's
// normal schema validation. It never changes ordinary property values.
func (i Identity) RebindDesiredState(raw json.RawMessage) json.RawMessage {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw
	}
	rebindOwner(doc, i.ManagedBy())
	out, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return out
}

func rebindOwner(value any, owner string) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if key == "Tags" {
				if pairs, ok := child.([]any); ok {
					for _, pair := range pairs {
						if tag, ok := pair.(map[string]any); ok && tag["Key"] == ManagedByKey {
							tag["Value"] = owner
						}
					}
				}
			}
			rebindOwner(child, owner)
		}
	case []any:
		for _, child := range value {
			rebindOwner(child, owner)
		}
	}
}
