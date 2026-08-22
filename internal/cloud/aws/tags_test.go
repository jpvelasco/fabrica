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
		typeName string
		state    string
		module   string
		version  string
		extra    map[string]string
		wantTags map[string]string
	}{
		{
			name:     "empty state gets tags added",
			typeName: "AWS::EC2::SecurityGroup",
			state:    `{}`,
			module:   "horde",
			version:  "0.1.0",
			wantTags: map[string]string{"ManagedBy": "fabrica", "FabricaModule": "horde", "FabricaVersion": "0.1.0"},
		},
		{
			name:     "empty raw message",
			typeName: "AWS::S3::Bucket",
			state:    "",
			module:   "perforce",
			version:  "0.1.0",
			wantTags: map[string]string{"ManagedBy": "fabrica", "FabricaModule": "perforce", "FabricaVersion": "0.1.0"},
		},
		{
			name:     "existing Tags array is preserved and new tags added",
			typeName: "AWS::S3::Bucket",
			state:    `{"Tags": [{"Key": "existing", "Value": "val"}], "Name": "my-bucket"}`,
			module:   "setup",
			version:  "0.2.0",
			wantTags: map[string]string{"existing": "val", "ManagedBy": "fabrica", "FabricaModule": "setup", "FabricaVersion": "0.2.0"},
		},
		{
			name:     "extra tags are merged",
			typeName: "AWS::EC2::SecurityGroup",
			state:    `{}`,
			module:   "horde",
			version:  "0.1.0",
			extra:    map[string]string{"env": "staging", "team": "platform"},
			wantTags: map[string]string{"ManagedBy": "fabrica", "FabricaModule": "horde", "FabricaVersion": "0.1.0", "env": "staging", "team": "platform"},
		},
		{
			name:     "standard tag overrides existing same-key tag",
			typeName: "AWS::EC2::SecurityGroup",
			state:    `{"Tags": [{"Key": "ManagedBy", "Value": "someone-else"}]}`,
			module:   "horde",
			version:  "0.1.0",
			wantTags: map[string]string{"ManagedBy": "fabrica", "FabricaModule": "horde", "FabricaVersion": "0.1.0"},
		},
		{
			name:     "non-json input returned unchanged",
			typeName: "AWS::EC2::SecurityGroup",
			state:    `not json`,
			module:   "horde",
			version:  "0.1.0",
			wantTags: nil,
		},
		{
			name:     "InstanceProfile denylist skips tags",
			typeName: "AWS::IAM::InstanceProfile",
			state:    `{"InstanceProfileName": "test-profile", "Roles": ["test-role"]}`,
			module:   "perforce",
			version:  "0.1.0",
			wantTags: nil,
		},
		{
			name:     "LaunchTemplate denylist skips top-level tags",
			typeName: "AWS::EC2::LaunchTemplate",
			state:    `{"LaunchTemplateName":"test","LaunchTemplateData":{"ImageId":"ami-123"}}`,
			module:   "horde",
			version:  "0.1.0",
			wantTags: nil,
		},
		{
			name:     "ScalingPolicy denylist skips tags",
			typeName: "AWS::AutoScaling::ScalingPolicy",
			state:    `{"PolicyName":"test-policy","AutoScalingGroupName":"test-asg"}`,
			module:   "horde",
			version:  "0.1.0",
			wantTags: nil,
		},
		{
			name:     "CloudWatchAlarm denylist skips tags",
			typeName: "AWS::CloudWatch::Alarm",
			state:    `{"AlarmName":"test-alarm","MetricName":"CPUUtilization"}`,
			module:   "horde",
			version:  "0.1.0",
			wantTags: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := injectFabricaTags(tt.typeName, json.RawMessage(tt.state), tt.module, tt.version, tt.extra)

			if tt.wantTags == nil {
				if string(result) != tt.state {
					t.Errorf("expected unchanged %q, got %s", tt.state, result)
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

// TestInjectFabricaTagsModuleShape guards the real-module case: desired states
// emit a Tags array already containing ManagedBy + Name (see
// internal/{perforce,horde,workstation,ci}/resources.go). The injected standard
// tags must merge by key — overriding ManagedBy in place, preserving Name, and
// never producing duplicate Key entries (which would be ambiguous to AWS).
func TestInjectFabricaTagsModuleShape(t *testing.T) {
	state := `{"GroupName":"fabrica-horde-sg","Tags":[{"Key":"ManagedBy","Value":"fabrica"},{"Key":"Name","Value":"fabrica-horde-sg"}]}`

	result := injectFabricaTags("AWS::EC2::SecurityGroup", json.RawMessage(state), "horde", "1.0.0", nil)

	var m map[string]any
	if err := json.Unmarshal(result, &m); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	arr, ok := m["Tags"].([]any)
	if !ok {
		t.Fatalf("Tags is not an array: %s", result)
	}

	// No duplicate keys.
	seen := map[string]int{}
	for _, item := range arr {
		obj := item.(map[string]any)
		seen[obj["Key"].(string)]++
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("tag key %q appears %d times, want 1: %s", k, n, result)
		}
	}

	tags := tagsAsMap(t, result)
	if tags["Name"] != "fabrica-horde-sg" {
		t.Errorf("Name tag not preserved: %q", tags["Name"])
	}
	if tags["ManagedBy"] != "fabrica" || tags["FabricaModule"] != "horde" {
		t.Errorf("standard tags missing/wrong: %+v", tags)
	}
	// Non-tag fields must survive untouched.
	if m["GroupName"] != "fabrica-horde-sg" {
		t.Errorf("GroupName clobbered: %v", m["GroupName"])
	}
}

// TestInjectFabricaTagsRespectsPlanModule verifies a plan-stamped FabricaModule
// (e.g. "lore") survives injection even though the provider passes its
// provider-level default ("fabrica") as the module value.
func TestInjectFabricaTagsRespectsPlanModule(t *testing.T) {
	state := `{"Tags":[{"Key":"ManagedBy","Value":"fabrica"},{"Key":"FabricaModule","Value":"lore"}]}`

	result := injectFabricaTags("AWS::EC2::SecurityGroup", json.RawMessage(state), "fabrica", "0.4.2", nil)

	tags := tagsAsMap(t, result)
	if tags["FabricaModule"] != "lore" {
		t.Errorf("FabricaModule = %q, want plan-stamped %q (must not be clobbered)", tags["FabricaModule"], "lore")
	}
	if tags["ManagedBy"] != "fabrica" {
		t.Errorf("ManagedBy = %q, want fabrica", tags["ManagedBy"])
	}
	// FabricaVersion is still injected when absent from the plan state.
	if tags["FabricaVersion"] != "0.4.2" {
		t.Errorf("FabricaVersion = %q, want 0.4.2", tags["FabricaVersion"])
	}
}
