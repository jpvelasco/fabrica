package aws

import (
	"encoding/json"
	"testing"
)

// tagsAsMap extracts the "Tags" array from a desired-state JSON document into a
// key→value map for assertions.
func tagsAsMap(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	arr, ok := m["Tags"].([]any)
	if !ok {
		t.Fatalf("no Tags array in result: %s", raw)
	}
	out := map[string]string{}
	for _, item := range arr {
		obj := item.(map[string]any)
		out[obj["Key"].(string)] = obj["Value"].(string)
	}
	return out
}

func TestInjectFabricaTags(t *testing.T) {
	tests := []struct {
		name     string
		state    string
		module   string
		version  string
		extra    map[string]string
		wantTags map[string]string
	}{
		{
			name:     "empty state gets tags added",
			state:    `{}`,
			module:   "horde",
			version:  "0.1.0",
			wantTags: map[string]string{"ManagedBy": "fabrica", "FabricaModule": "horde", "FabricaVersion": "0.1.0"},
		},
		{
			name:     "empty raw message",
			state:    "",
			module:   "perforce",
			version:  "0.1.0",
			wantTags: map[string]string{"ManagedBy": "fabrica", "FabricaModule": "perforce", "FabricaVersion": "0.1.0"},
		},
		{
			name:     "existing Tags array is preserved and new tags added",
			state:    `{"Tags": [{"Key": "existing", "Value": "val"}], "Name": "my-bucket"}`,
			module:   "setup",
			version:  "0.2.0",
			wantTags: map[string]string{"existing": "val", "ManagedBy": "fabrica", "FabricaModule": "setup", "FabricaVersion": "0.2.0"},
		},
		{
			name:     "extra tags are merged",
			state:    `{}`,
			module:   "horde",
			version:  "0.1.0",
			extra:    map[string]string{"env": "staging", "team": "platform"},
			wantTags: map[string]string{"ManagedBy": "fabrica", "FabricaModule": "horde", "FabricaVersion": "0.1.0", "env": "staging", "team": "platform"},
		},
		{
			name:     "standard tag overrides existing same-key tag",
			state:    `{"Tags": [{"Key": "ManagedBy", "Value": "someone-else"}]}`,
			module:   "horde",
			version:  "0.1.0",
			wantTags: map[string]string{"ManagedBy": "fabrica", "FabricaModule": "horde", "FabricaVersion": "0.1.0"},
		},
		{
			name:     "non-json input returned unchanged",
			state:    `not json`,
			module:   "horde",
			version:  "0.1.0",
			wantTags: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := injectFabricaTags(json.RawMessage(tt.state), tt.module, tt.version, tt.extra)

			if tt.wantTags == nil {
				if string(result) != tt.state {
					t.Errorf("expected unchanged, got %s", result)
				}
				return
			}

			// Must not emit a lowercase "tags" key (Cloud Control rejects it).
			var m map[string]any
			if err := json.Unmarshal(result, &m); err != nil {
				t.Fatalf("result is not JSON: %v", err)
			}
			if _, bad := m["tags"]; bad {
				t.Errorf("result must not contain lowercase 'tags' key: %s", result)
			}

			tags := tagsAsMap(t, result)
			for k, v := range tt.wantTags {
				if tags[k] != v {
					t.Errorf("tag %s = %q, want %q", k, tags[k], v)
				}
			}
		})
	}
}
