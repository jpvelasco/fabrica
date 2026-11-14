package tags

import (
	"encoding/json"
	"testing"
)

func TestRebindDesiredStatePreservesSchemas(t *testing.T) {
	identity, err := ResolveIdentity("Studio-Tools")
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"Tags":[{"Key":"ManagedBy","Value":"fabrica","PropagateAtLaunch":true}],"LaunchTemplateData":{"TagSpecifications":[{"ResourceType":"instance","Tags":[{"Key":"ManagedBy","Value":"old"},{"Key":"Name","Value":"fabrica"}]}]},"Other":{"Key":"ManagedBy","Value":"fabrica"},"TagsElsewhere":"fabrica"}`)
	got := identity.RebindDesiredState(raw)
	want := json.RawMessage(`{"Tags":[{"Key":"ManagedBy","Value":"Studio-Tools","PropagateAtLaunch":true}],"LaunchTemplateData":{"TagSpecifications":[{"ResourceType":"instance","Tags":[{"Key":"ManagedBy","Value":"Studio-Tools"},{"Key":"Name","Value":"fabrica"}]}]},"Other":{"Key":"ManagedBy","Value":"fabrica"},"TagsElsewhere":"fabrica"}`)
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	if string(aJSON) != string(bJSON) {
		t.Fatalf("schema changed: %s", got)
	}
	for _, raw := range []json.RawMessage{nil, []byte(`{`), []byte(`{"Tags":"unknown"}`), []byte(`{"Tags":["unknown",{"Key":"Name","Value":"keep"}]} `)} {
		if got := identity.RebindDesiredState(raw); len(raw) > 0 && len(got) == 0 {
			t.Fatal("document lost")
		}
	}
}
