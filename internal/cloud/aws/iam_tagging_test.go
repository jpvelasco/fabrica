package aws

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// fakeIAMClient implements iamAPIClient for testing: records the last
// TagInstanceProfile input, or returns tagErr.
type fakeIAMClient struct {
	tagErr    error
	calls     int
	lastInput *iam.TagInstanceProfileInput
}

func (f *fakeIAMClient) TagInstanceProfile(_ context.Context, in *iam.TagInstanceProfileInput, _ ...func(*iam.Options)) (*iam.TagInstanceProfileOutput, error) {
	if f.tagErr != nil {
		return nil, f.tagErr
	}
	f.calls++
	f.lastInput = in
	return &iam.TagInstanceProfileOutput{}, nil
}

func newIamTaggingTest(t *testing.T, fake *fakeIAMClient) *iamService {
	t.Helper()
	return &iamService{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{Region: "us-east-1"}, nil
		},
		newClient: func(awssdk.Config) iamAPIClient { return fake },
	}
}

func TestTagInstanceProfileSuccess(t *testing.T) {
	fake := &fakeIAMClient{}
	s := newIamTaggingTest(t, fake)

	err := s.TagInstanceProfile(context.Background(), "fabrica-perforce-profile", map[string]string{
		"ManagedBy":     "fabrica",
		"FabricaModule": "perforce",
		"env":           "staging",
	})
	if err != nil {
		t.Fatalf("TagInstanceProfile: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("TagInstanceProfile calls = %d, want 1", fake.calls)
	}
	if awssdk.ToString(fake.lastInput.InstanceProfileName) != "fabrica-perforce-profile" {
		t.Errorf("profile name = %q", awssdk.ToString(fake.lastInput.InstanceProfileName))
	}
	got := map[string]string{}
	for _, tg := range fake.lastInput.Tags {
		got[awssdk.ToString(tg.Key)] = awssdk.ToString(tg.Value)
	}
	want := map[string]string{"ManagedBy": "fabrica", "FabricaModule": "perforce", "env": "staging"}
	if len(got) != len(want) {
		t.Errorf("tags = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("tag %s = %q, want %q (tags: %v)", k, got[k], v, got)
		}
	}
}

func TestTagInstanceProfileNoopInputs(t *testing.T) {
	fake := &fakeIAMClient{}
	s := newIamTaggingTest(t, fake)

	if err := s.TagInstanceProfile(context.Background(), "p", nil); err != nil {
		t.Fatalf("empty tags: %v", err)
	}
	if fake.calls != 0 {
		t.Errorf("calls = %d, want 0 (no tags, no API call)", fake.calls)
	}
}

func TestTagInstanceProfileAPIError(t *testing.T) {
	fake := &fakeIAMClient{tagErr: errors.New("access denied")}
	s := newIamTaggingTest(t, fake)

	err := s.TagInstanceProfile(context.Background(), "fabrica-horde-profile", map[string]string{"ManagedBy": "fabrica"})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	assertStringContains(t, err.Error(), "tagging IAM instance profile fabrica-horde-profile")
	assertStringContains(t, err.Error(), "access denied")
}

// TestIamServiceRealSeamDefaults exercises the non-seam path: with no test
// seams the service falls back to the real AWS config loader and SDK
// constructor. Both resolve locally (LoadDefaultConfig reads no API, and
// iam.NewFromConfig constructs without dialing AWS).
func TestIamServiceRealSeamDefaults(t *testing.T) {
	s := &iamService{awsCfg: awsConfig{region: "us-east-1"}}
	if err := s.ensureClient(context.Background()); err != nil {
		t.Fatalf("ensureClient: %v", err)
	}
	if s.client == nil {
		t.Fatal("client not constructed through the real seams")
	}
}

func TestTagInstanceProfileLoadConfigError(t *testing.T) {
	fake := &fakeIAMClient{}
	s := newIamTaggingTest(t, fake)
	s.loadCfg = func(context.Context, string, string) (awssdk.Config, error) {
		return awssdk.Config{}, errors.New("no credentials")
	}

	err := s.TagInstanceProfile(context.Background(), "p", map[string]string{"ManagedBy": "fabrica"})
	if err == nil {
		t.Fatal("want config error, got nil")
	}
	assertStringContains(t, err.Error(), "loading AWS config for IAM service")
	assertStringContains(t, err.Error(), "no credentials")
}
