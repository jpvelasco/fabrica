package aws

import (
	"encoding/json"
	"sort"

	fabricatags "github.com/jpvelasco/fabrica/internal/tags"
)

// tagUnsupportedTypes lists Cloud Control resource types that do not accept a
// "Tags" property in their desired-state schema. The Cloud Control provider for
// these types rejects the key as extraneous, even though CloudFormation or the
// SDK may support tagging via separate APIs.
var tagUnsupportedTypes = map[string]struct{}{
	"AWS::IAM::InstanceProfile": {},
	// AWS::EC2::LaunchTemplate does not accept a top-level "Tags" key in the
	// Cloud Control schema — tags go in TagSpecifications only.
	"AWS::EC2::LaunchTemplate": {},
	// AWS::EC2::SecurityGroupIngress has no "Tags" property in the Cloud
	// Control schema — injecting Tags causes validation rejection.
	"AWS::EC2::SecurityGroupIngress": {},
	// AWS::AutoScaling::AutoScalingGroup tags require PropagateAtLaunch;
	// the plan layer sets this correctly. The generic injector would add
	// tags without it, causing Cloud Control validation errors.
	"AWS::AutoScaling::AutoScalingGroup": {},
	// AWS::AutoScaling::ScalingPolicy has no "Tags" property in the Cloud
	// Control schema — injecting Tags causes validation rejection.
	"AWS::AutoScaling::ScalingPolicy": {},
	// AWS::CloudWatch::Alarm has no "Tags" property in the Cloud Control
	// schema — injecting Tags causes validation rejection.
	"AWS::CloudWatch::Alarm": {},
}

// Identity tag protection lives in internal/tags (fabricatags.IdentityKey,
// fabricatags.MergeProtected) so the Cloud Control path and the post-create
// SDK tagging paths (instance profiles, security group rules, SSM
// parameters) share one rule: operator tags are additive, identity tags are
// Fabrica-owned.

// injectFabricaTags merges standard Fabrica tags into the desired state of a
// resource. Cloud Control resource schemas (IAM, CodeBuild, EC2, DynamoDB, ...)
// represent tags as a capitalized "Tags" array of {Key, Value} objects, and
// reject any extraneous lowercase "tags" key — so we merge into "Tags" in that
// shape. Existing tags are preserved; standard/extra tags override by key,
// except reserved identity tags, which operator tags cannot shadow.
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

	pairs := existingTagPairs(m["Tags"])
	base := make(map[string]string, len(pairs))
	for _, p := range pairs {
		base[p.Key] = p.Value
	}
	merged := fabricatags.MergeProtected(module, version, base, extra)

	// Rebuild the "Tags" array preserving first-seen key order for stable
	// output: existing keys, then identity keys, then sorted operator keys.
	// Plans stamp their own FabricaModule (e.g. "lore", "ddc", "horde") —
	// the protected merge keeps that value instead of clobbering it with the
	// provider-level default, so live tags support per-module attribution.
	order := make([]string, 0, len(merged))
	seen := make(map[string]bool, len(merged))
	add := func(k string) {
		if !seen[k] {
			seen[k] = true
			order = append(order, k)
		}
	}
	for _, p := range pairs {
		add(p.Key)
	}
	add("ManagedBy")
	add("FabricaVersion")
	if _, ok := base["FabricaModule"]; !ok {
		add("FabricaModule")
	}
	extraKeys := make([]string, 0, len(extra))
	for k := range extra {
		if k != "" && !fabricatags.IdentityKey(k) {
			extraKeys = append(extraKeys, k)
		}
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		add(k)
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

// postCreateTags returns the tag set to apply to a resource whose Cloud
// Control create schema cannot carry tags. It prefers the final (post-
// injectFabricaTags) desired-state "Tags" array — the exact set applied to
// the resource — and falls back to the protected standard set for types
// whose schemas carry no Tags (IAM instance profiles, standalone ingress
// rules).
func postCreateTags(desiredState json.RawMessage, version string, extra map[string]string) map[string]string {
	if len(desiredState) > 0 {
		var m map[string]any
		if err := json.Unmarshal(desiredState, &m); err == nil {
			pairs := existingTagPairs(m["Tags"])
			if len(pairs) > 0 {
				out := make(map[string]string, len(pairs))
				for _, p := range pairs {
					out[p.Key] = p.Value
				}
				return out
			}
		}
	}
	return fabricatags.MergeProtected("fabrica", version, nil, extra)
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
