package aws

import (
	"context"
	"encoding/json"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

// desiredStateTags parses a CreateResource input's DesiredState and returns its
// Tags array as a key→value map.
func desiredStateTags(t *testing.T, in *cloudcontrol.CreateResourceInput) map[string]string {
	t.Helper()
	if in == nil || in.DesiredState == nil {
		t.Fatalf("CreateResource input or DesiredState is nil")
	}
	return tagsAsMap(t, json.RawMessage(awssdk.ToString(in.DesiredState)))
}

// TestCreate_MergesOperatorTags verifies cloud.aws.tags (carried as
// resourceClients.operatorTags) are merged onto Cloud Control creates, while
// reserved identity tags stay Fabrica-owned — operator tags are additive and
// cannot shadow them. Closes #417.
func TestCreate_MergesOperatorTags(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
		},
	}
	waiter := &fakeCCWaiter{
		out: &cloudcontrol.GetResourceRequestStatusOutput{
			ProgressEvent: &types.ProgressEvent{
				OperationStatus: types.OperationStatusSuccess,
				Identifier:      awssdk.String("sg-ops-1"),
			},
		},
	}
	rc := newCCTestClients(client, waiter)
	rc.operatorTags = map[string]string{
		"env":            "staging",
		"team":           "platform",
		"ManagedBy":      "operator-override",
		"FabricaModule":  "operator-module",
		"FabricaVersion": "operator-version",
	}

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroup",
		DesiredState: json.RawMessage(`{"GroupName":"fabrica-horde-sg"}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	tags := desiredStateTags(t, client.createInputs[0])
	if tags["env"] != "staging" {
		t.Errorf("operator tag env = %q, want staging (tags: %v)", tags["env"], tags)
	}
	if tags["team"] != "platform" {
		t.Errorf("operator tag team = %q, want platform (tags: %v)", tags["team"], tags)
	}
	// Reserved identity tags are Fabrica-owned: operator tags of the same key
	// must not shadow them (teardown/drift/cost key off these).
	if tags["ManagedBy"] != "fabrica" {
		t.Errorf("ManagedBy = %q, want fabrica (tags: %v)", tags["ManagedBy"], tags)
	}
	if tags["FabricaModule"] != "fabrica" {
		t.Errorf("FabricaModule = %q, want fabrica (tags: %v)", tags["FabricaModule"], tags)
	}
	if tags["FabricaVersion"] != "test" {
		t.Errorf("FabricaVersion = %q, want test (tags: %v)", tags["FabricaVersion"], tags)
	}
}

// TestCreate_NoOperatorTagsUnchanged verifies a nil operatorTags map leaves the
// tag set exactly as injectFabricaTags produced it (no empty-key entries).
func TestCreate_NoOperatorTagsUnchanged(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
		},
	}
	waiter := &fakeCCWaiter{
		out: &cloudcontrol.GetResourceRequestStatusOutput{
			ProgressEvent: &types.ProgressEvent{
				OperationStatus: types.OperationStatusSuccess,
				Identifier:      awssdk.String("sg-noops"),
			},
		},
	}
	rc := newCCTestClients(client, waiter)

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroup",
		DesiredState: json.RawMessage(`{"GroupName":"fabrica-horde-sg"}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	tags := desiredStateTags(t, client.createInputs[0])
	want := map[string]string{"ManagedBy": "fabrica", "FabricaModule": "fabrica", "FabricaVersion": "test"}
	if len(tags) != len(want) {
		t.Errorf("tag count = %d, want %d (tags: %v)", len(tags), len(want), tags)
	}
	for k, v := range want {
		if tags[k] != v {
			t.Errorf("tag %s = %q, want %q", k, tags[k], v)
		}
	}
}

// TestCreateAsync_MergesOperatorTags verifies the async create path (GameLift
// fleets) carries operator tags too.
func TestCreateAsync_MergesOperatorTags(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{
				Identifier: awssdk.String("fleet-ops-1"),
			},
		},
	}
	rc := newCCTestClients(client, nil)
	rc.operatorTags = map[string]string{"run": "phase-a"}

	r := &fabricac.Resource{
		TypeName:     "AWS::GameLift::Fleet",
		DesiredState: json.RawMessage(`{"Name":"test-fleet"}`),
	}
	if err := rc.createAsync(context.Background(), r); err != nil {
		t.Fatalf("createAsync: %v", err)
	}
	tags := desiredStateTags(t, client.createInputs[0])
	if tags["run"] != "phase-a" {
		t.Errorf("operator tag run = %q, want phase-a (tags: %v)", tags["run"], tags)
	}
	if tags["FabricaVersion"] != "test" {
		t.Errorf("FabricaVersion = %q, want test (tags: %v)", tags["FabricaVersion"], tags)
	}
}

// TestOperatorTagsOf verifies the config→provider copy: set tags are copied
// verbatim, unset/empty config yields nil.
func TestOperatorTagsOf(t *testing.T) {
	cfg := &config.Config{}
	cfg.Cloud.AWS.Tags = map[string]string{"env": "prod", "team": "platform"}
	got := operatorTagsOf(cfg)
	if len(got) != 2 || got["env"] != "prod" || got["team"] != "platform" {
		t.Errorf("operatorTagsOf(set) = %v, want the config tags copied", got)
	}

	empty := &config.Config{}
	if got := operatorTagsOf(empty); got != nil {
		t.Errorf("operatorTagsOf(empty) = %v, want nil", got)
	}
}

// TestNewProvider_PopulatesOperatorTags verifies newProvider copies
// cloud.aws.tags into the provider's resource clients.
func TestNewProvider_PopulatesOperatorTags(t *testing.T) {
	cfg := &config.Config{}
	cfg.Cloud.AWS.Tags = map[string]string{"env": "prod"}
	p, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	ap := p.(*awsProvider)
	if got := ap.clients.operatorTags["env"]; got != "prod" {
		t.Errorf("clients.operatorTags[env] = %q, want prod", got)
	}

	def := config.Defaults()
	p2, err := newProvider(def)
	if err != nil {
		t.Fatalf("newProvider(defaults): %v", err)
	}
	if p2.(*awsProvider).clients.operatorTags != nil {
		t.Errorf("clients.operatorTags = %v, want nil for default config", p2.(*awsProvider).clients.operatorTags)
	}
}

// TestWithRegion_CopiesOperatorTags verifies the multi-region view carries the
// operator tags (DDC edge creates must be tagged too).
func TestWithRegion_CopiesOperatorTags(t *testing.T) {
	cfg := &config.Config{}
	cfg.Cloud.AWS.Tags = map[string]string{"env": "edge"}
	p, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	ap := p.(*awsProvider)
	view, err := ap.WithRegion(context.Background(), "eu-west-1")
	if err != nil {
		t.Fatalf("WithRegion: %v", err)
	}
	rc, ok := view.Resources.(*resourceClients)
	if !ok {
		t.Fatalf("view.Resources type = %T, want *resourceClients", view.Resources)
	}
	if rc.operatorTags["env"] != "edge" {
		t.Errorf("scoped operatorTags[env] = %q, want edge", rc.operatorTags["env"])
	}
	// The source provider must not have been mutated.
	if ap.clients.operatorTags["env"] != "edge" {
		t.Errorf("source operatorTags[env] = %q, want edge", ap.clients.operatorTags["env"])
	}
}
