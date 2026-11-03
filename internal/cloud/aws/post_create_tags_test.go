package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

// postCreateCreateFakes wires a successful Cloud Control create whose waiter
// returns the given identifier.
func postCreateCreateFakes(identifier string) (*fakeCCClient, *fakeCCWaiter) {
	cc := &fakeCCClient{createOut: &cloudcontrol.CreateResourceOutput{
		ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
	}}
	waiter := &fakeCCWaiter{out: &cloudcontrol.GetResourceRequestStatusOutput{
		ProgressEvent: &types.ProgressEvent{
			OperationStatus: types.OperationStatusSuccess,
			Identifier:      awssdk.String(identifier),
		},
	}}
	return cc, waiter
}

// TestCreateInstanceProfileTagsPostCreate verifies the post-create hook tags
// IAM instance profiles (whose create schema has no Tags property) with the
// protected standard set plus additive operator tags — operator entries that
// shadow identity tags are rejected.
func TestCreateInstanceProfileTagsPostCreate(t *testing.T) {
	cc, waiter := postCreateCreateFakes("fabrica-perforce-profile")
	rc := newCCTestClients(cc, waiter)
	iamFake := &fakeIAMClient{}
	rc.iam = &iamService{client: iamFake}
	rc.operatorTags = map[string]string{"env": "staging", "ManagedBy": "operator-override"}

	r := &fabricac.Resource{
		TypeName:     "AWS::IAM::InstanceProfile",
		DesiredState: json.RawMessage(`{"InstanceProfileName":"fabrica-perforce-profile","Roles":["fabrica-perforce-role"]}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if iamFake.calls != 1 {
		t.Fatalf("TagInstanceProfile calls = %d, want 1", iamFake.calls)
	}
	if awssdk.ToString(iamFake.lastInput.InstanceProfileName) != "fabrica-perforce-profile" {
		t.Errorf("profile name = %q", awssdk.ToString(iamFake.lastInput.InstanceProfileName))
	}
	got := map[string]string{}
	for _, tg := range iamFake.lastInput.Tags {
		got[awssdk.ToString(tg.Key)] = awssdk.ToString(tg.Value)
	}
	want := map[string]string{"ManagedBy": "fabrica", "FabricaModule": "fabrica", "FabricaVersion": "test", "env": "staging"}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("tag %s = %q, want %q (tags: %v)", k, got[k], v, got)
		}
	}
}

// TestCreateInstanceProfileTagErrorFailsCreate verifies an IAM tagging error
// fails the create so the operator sees the resource exists but is
// untagged — the re-run re-enters the hook via AlreadyExists recovery.
func TestCreateInstanceProfileTagErrorFailsCreate(t *testing.T) {
	cc, waiter := postCreateCreateFakes("fabrica-horde-profile")
	rc := newCCTestClients(cc, waiter)
	rc.iam = &iamService{client: &fakeIAMClient{tagErr: errors.New("access denied")}}

	r := &fabricac.Resource{
		TypeName:     "AWS::IAM::InstanceProfile",
		DesiredState: json.RawMessage(`{"InstanceProfileName":"fabrica-horde-profile","Roles":["fabrica-horde-role"]}`),
	}
	err := rc.Create(context.Background(), r)
	if err == nil {
		t.Fatal("want tagging error, got nil")
	}
	assertStringContains(t, err.Error(), "tagging IAM instance profile fabrica-horde-profile")
}

// TestPostCreateTags covers the tag-set resolution: the final desired-state
// "Tags" array wins when present; otherwise the protected standard set with
// operator extras applies.
func TestPostCreateTags(t *testing.T) {
	// With Tags in the doc: the exact set applied to the resource.
	withTags := json.RawMessage(`{"Tags":[{"Key":"ManagedBy","Value":"fabrica"},{"Key":"FabricaModule","Value":"ddc"},{"Key":"env","Value":"prod"}]}`)
	got := postCreateTags(withTags, "0.5.0", map[string]string{"team": "platform"})
	if got["FabricaModule"] != "ddc" || got["env"] != "prod" {
		t.Errorf("doc Tags must win: %v", got)
	}

	// Without Tags: the protected standard set + additive operator extras.
	fallback := postCreateTags(json.RawMessage(`{"InstanceProfileName":"p"}`), "0.5.0", map[string]string{"team": "platform", "ManagedBy": "operator"})
	want := map[string]string{"ManagedBy": "fabrica", "FabricaModule": "fabrica", "FabricaVersion": "0.5.0", "team": "platform"}
	if len(fallback) != len(want) {
		t.Errorf("fallback = %v, want %v", fallback, want)
	}
	for k, v := range want {
		if fallback[k] != v {
			t.Errorf("fallback tag %s = %q, want %q (tags: %v)", k, fallback[k], v, fallback)
		}
	}

	// Non-JSON: the protected standard set.
	if got := postCreateTags(json.RawMessage(`not json`), "0.5.0", nil); got["ManagedBy"] != "fabrica" || got["FabricaVersion"] != "0.5.0" {
		t.Errorf("non-JSON fallback = %v", got)
	}
}

// TestCreateInstanceProfileSkipsHookWithoutIAMClient: providers without the
// IAM auxiliary seam (fakes, E2E) still complete instance-profile creates —
// the post-create hook is a no-op there.
func TestCreateInstanceProfileSkipsHookWithoutIAMClient(t *testing.T) {
	cc, waiter := postCreateCreateFakes("fabrica-perforce-profile")
	rc := newCCTestClients(cc, waiter)

	r := &fabricac.Resource{
		TypeName:     "AWS::IAM::InstanceProfile",
		DesiredState: json.RawMessage(`{"InstanceProfileName":"fabrica-perforce-profile"}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.Identifier != "fabrica-perforce-profile" {
		t.Errorf("identifier = %q, want the waiter-provided value", r.Identifier)
	}
}
