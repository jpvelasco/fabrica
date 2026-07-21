package aws

import (
	"encoding/json"
)

// tagUnsupportedTypes lists Cloud Control resource types that do not accept a
// "Tags" property in their desired-state schema. The Cloud Control provider for
// these types rejects the key as extraneous, even though CloudFormation or the
// SDK may support tagging via separate APIs.
var tagUnsupportedTypes = map[string]struct{}{
	"AWS::IAM::InstanceProfile": {},
}

// injectFabricaTags merges standard Fabrica tags into the desired state of a
// resource. Cloud Control resource schemas (IAM, CodeBuild, EC2, DynamoDB, ...)
// represent tags as a capitalized "Tags" array of {Key, Value} objects, and
// reject any extraneous lowercase "tags" key — so we merge into "Tags" in that
// shape. Existing tags are preserved; standard/extra tags override by key.
// Returns the state unchanged for resource types that don't support tags.
func injectFabricaTags(typeName string, state json.RawMessage, module, version string, extra map[string]string) json.RawMessage {
	if _, unsupported := tagUnsupportedTypes[typeName]; unsupported {
		return state
	}
	if len(state) == 0 {
		state = json.RawMessage(`{}`)
	}

	var m map[string]any
	if err := json.Unmarshal(state, &m); err != nil {
		// If we can't parse the desired state as JSON, return it unchanged.
		return state
	}

	// Build the merged key→value set, seeded from any existing "Tags" array.
	merged := map[string]string{}
	order := []string{}
	addKey := func(k string) {
		if _, seen := merged[k]; !seen {
			order = append(order, k)
		}
	}

	for _, raw := range existingTagPairs(m["Tags"]) {
		addKey(raw.Key)
		merged[raw.Key] = raw.Value
	}

	standard := []struct{ k, v string }{
		{"ManagedBy", "fabrica"},
		{"FabricaModule", module},
		{"FabricaVersion", version},
	}
	for _, s := range standard {
		addKey(s.k)
		merged[s.k] = s.v
	}
	for k, v := range extra {
		addKey(k)
		merged[k] = v
	}

	tags := make([]map[string]string, 0, len(order))
	for _, k := range order {
		tags = append(tags, map[string]string{"Key": k, "Value": merged[k]})
	}
	m["Tags"] = tags

	out, err := json.Marshal(m)
	if err != nil {
		return state
	}
	return out
}

type tagPair struct {
	Key   string
	Value string
}

// existingTagPairs extracts {Key, Value} pairs from a desired-state "Tags"
// value, tolerating the [{"Key":..,"Value":..}] array shape. Unknown shapes
// yield no pairs (the standard tags are still applied).
func existingTagPairs(v any) []tagPair {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	pairs := make([]tagPair, 0, len(arr))
	for _, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		key, ok := obj["Key"].(string)
		if !ok {
			continue
		}
		val, _ := obj["Value"].(string)
		pairs = append(pairs, tagPair{Key: key, Value: val})
	}
	return pairs
}
