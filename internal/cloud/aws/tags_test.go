package aws

import (
	"encoding/json"
	"testing"
)

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
			extra:    nil,
			wantTags: map[string]string{"ManagedBy": "fabrica", "FabricaModule": "horde", "FabricaVersion": "0.1.0"},
		},
		{
			name:     "empty raw message",
			state:    "",
			module:   "perforce",
			version:  "0.1.0",
			extra:    nil,
			wantTags: map[string]string{"ManagedBy": "fabrica", "FabricaModule": "perforce", "FabricaVersion": "0.1.0"},
		},
		{
			name:     "existing tags are preserved and new tags added",
			state:    `{"tags": {"existing": "val"}, "name": "my-bucket"}`,
			module:   "setup",
			version:  "0.2.0",
			extra:    nil,
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
			name:     "non-json input returned unchanged",
			state:    `not json`,
			module:   "horde",
			version:  "0.1.0",
			extra:    nil,
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

			var m map[string]any
			if err := json.Unmarshal(result, &m); err != nil {
				t.Fatalf("result is not JSON: %v", err)
			}

			tags, ok := m["tags"].(map[string]any)
			if !ok {
				t.Fatalf("no tags field in result: %s", result)
			}

			for k, v := range tt.wantTags {
				if tags[k] != v {
					t.Errorf("tag %s = %v, want %q", k, tags[k], v)
				}
			}
		})
	}
}
