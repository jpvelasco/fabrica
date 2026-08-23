package aws

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// fakeVolumeEC2Client drives TagInstanceVolumes: scripted DescribeVolumes
// pages plus a CreateTags recorder.
type fakeVolumeEC2Client struct {
	stubEC2
	pages       []ec2.DescribeVolumesOutput
	describeErr error
	createErr   error
	calls       int
	tagCalls    int
	lastTags    []types.Tag
	lastVolumes []string
}

func (f *fakeVolumeEC2Client) DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	i := f.calls
	f.calls++
	if i >= len(f.pages) {
		return &ec2.DescribeVolumesOutput{}, nil
	}
	return &f.pages[i], nil
}

func (f *fakeVolumeEC2Client) CreateTags(_ context.Context, in *ec2.CreateTagsInput, _ ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.tagCalls++
	f.lastVolumes = append(f.lastVolumes, in.Resources...)
	f.lastTags = in.Tags
	return &ec2.CreateTagsOutput{}, nil
}

func newVolumeTaggerTest(t *testing.T, fake *fakeVolumeEC2Client) *ec2Service {
	t.Helper()
	return &ec2Service{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			return aws.Config{Region: "us-east-1"}, nil
		},
		newClient: func(aws.Config) ec2APIClient { return fake },
	}
}

func TestTagInstanceVolumes(t *testing.T) {
	fake := &fakeVolumeEC2Client{pages: []ec2.DescribeVolumesOutput{
		{Volumes: []types.Volume{
			{VolumeId: aws.String("vol-1")},
			{VolumeId: aws.String("vol-2")},
		}},
	}}
	s := newVolumeTaggerTest(t, fake)

	err := s.TagInstanceVolumes(context.Background(), "i-1", map[string]string{
		"ManagedBy": "fabrica", "FabricaModule": "perforce",
	})
	if err != nil {
		t.Fatalf("TagInstanceVolumes: %v", err)
	}
	if len(fake.lastVolumes) != 2 || fake.lastVolumes[0] != "vol-1" || fake.lastVolumes[1] != "vol-2" {
		t.Errorf("tagged volumes = %v, want [vol-1 vol-2]", fake.lastVolumes)
	}
	byKey := map[string]string{}
	for _, tg := range fake.lastTags {
		byKey[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}
	if byKey["FabricaModule"] != "perforce" || byKey["ManagedBy"] != "fabrica" {
		t.Errorf("applied tags = %v", byKey)
	}
}

func TestTagInstanceVolumesRetriesUntilAttached(t *testing.T) {
	// First describe sees nothing (attach race), second sees the volume.
	fake := &fakeVolumeEC2Client{pages: []ec2.DescribeVolumesOutput{
		{},
		{Volumes: []types.Volume{{VolumeId: aws.String("vol-late")}}},
	}}
	s := newVolumeTaggerTest(t, fake)

	if err := s.TagInstanceVolumes(context.Background(), "i-1", map[string]string{"ManagedBy": "fabrica"}); err != nil {
		t.Fatalf("TagInstanceVolumes: %v", err)
	}
	if fake.calls != 2 {
		t.Errorf("describe calls = %d, want 2 (attach retry)", fake.calls)
	}
	if len(fake.lastVolumes) != 1 || fake.lastVolumes[0] != "vol-late" {
		t.Errorf("tagged = %v", fake.lastVolumes)
	}
}

func TestTagInstanceVolumesNoTagsNoop(t *testing.T) {
	fake := &fakeVolumeEC2Client{}
	s := newVolumeTaggerTest(t, fake)
	if err := s.TagInstanceVolumes(context.Background(), "i-1", nil); err != nil {
		t.Fatalf("TagInstanceVolumes with no tags: %v", err)
	}
	if fake.tagCalls != 0 || fake.calls != 0 {
		t.Errorf("no-tag call must be a no-op; describes=%d tagCalls=%d", fake.calls, fake.tagCalls)
	}
}

func TestTagInstanceVolumesDescribeErrorWrapped(t *testing.T) {
	fake := &fakeVolumeEC2Client{describeErr: context.DeadlineExceeded}
	s := newVolumeTaggerTest(t, fake)
	err := s.TagInstanceVolumes(context.Background(), "i-1", map[string]string{"k": "v"})
	if err == nil || !strings.Contains(err.Error(), "describing volumes attached to i-1") {
		t.Errorf("err = %v, want describe-phase context", err)
	}
}

func TestTagInstanceVolumesCreateErrorWrapped(t *testing.T) {
	fake := &fakeVolumeEC2Client{
		pages:     []ec2.DescribeVolumesOutput{{Volumes: []types.Volume{{VolumeId: aws.String("vol-x")}}}},
		createErr: errors.New("unauthorized"),
	}
	s := newVolumeTaggerTest(t, fake)
	err := s.TagInstanceVolumes(context.Background(), "i-1", map[string]string{"k": "v"})
	if err == nil || !strings.Contains(err.Error(), "tagging volume vol-x") {
		t.Errorf("err = %v, want tagging-phase context", err)
	}
}

func TestTagInstanceVolumesCtxCancelledDuringRetry(t *testing.T) {
	fake := &fakeVolumeEC2Client{} // never returns volumes
	s := newVolumeTaggerTest(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := s.TagInstanceVolumes(ctx, "i-1", map[string]string{"ManagedBy": "fabrica"})
	if err == nil || !strings.Contains(err.Error(), "waiting for volumes on i-1") {
		t.Fatalf("err = %v, want retry-wait cancellation error", err)
	}
}

func TestTagInstanceVolumesEnsureClientError(t *testing.T) {
	s := &ec2Service{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			return aws.Config{}, errors.New("no credentials")
		},
	}
	err := s.TagInstanceVolumes(context.Background(), "i-1", map[string]string{"k": "v"})
	if err == nil || !strings.Contains(err.Error(), "loading AWS config") {
		t.Fatalf("err = %v, want config-load failure", err)
	}
}
