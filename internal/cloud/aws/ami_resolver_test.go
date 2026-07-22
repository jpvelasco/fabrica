package aws

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

// fakeEC2ImagesClient implements ec2APIClient for AMI resolver tests.
type fakeEC2ImagesClient struct {
	images []types.Image
	err    error
}

func (f *fakeEC2ImagesClient) StopInstances(context.Context, *ec2.StopInstancesInput, ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	return &ec2.StopInstancesOutput{}, nil
}
func (f *fakeEC2ImagesClient) StartInstances(context.Context, *ec2.StartInstancesInput, ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	return &ec2.StartInstancesOutput{}, nil
}
func (f *fakeEC2ImagesClient) DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{}, nil
}
func (f *fakeEC2ImagesClient) DescribeImages(context.Context, *ec2.DescribeImagesInput, ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &ec2.DescribeImagesOutput{Images: f.images}, nil
}

func TestResolveUbuntuAMI_Success(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	yesterday := time.Now().AddDate(0, 0, -1).UTC().Format(time.RFC3339)

	fake := &fakeEC2ImagesClient{
		images: []types.Image{
			{ImageId: aws.String("ami-old"), CreationDate: aws.String(yesterday)},
			{ImageId: aws.String("ami-new"), CreationDate: aws.String(now)},
		},
	}

	resolver := &amiResolver{client: fake}
	got, err := resolver.ResolveUbuntuAMI(context.Background(), "us-east-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ami-new" {
		t.Errorf("got %q, want ami-new (most recent)", got)
	}
}

func TestResolveUbuntuAMI_NoImages(t *testing.T) {
	fake := &fakeEC2ImagesClient{images: nil}
	resolver := &amiResolver{client: fake}

	_, err := resolver.ResolveUbuntuAMI(context.Background(), "us-east-1")
	if err == nil {
		t.Fatal("expected error when no images found")
	}
	if !containsString(err.Error(), "no Ubuntu 22.04 AMI found") {
		t.Fatalf("error %q does not mention missing AMI", err.Error())
	}
}

func TestResolveUbuntuAMI_DescribeError(t *testing.T) {
	fake := &fakeEC2ImagesClient{err: errors.New("access denied")}
	resolver := &amiResolver{client: fake}

	_, err := resolver.ResolveUbuntuAMI(context.Background(), "us-east-1")
	if err == nil {
		t.Fatal("expected error on describe failure")
	}
	if !containsString(err.Error(), "describing Ubuntu AMIs") {
		t.Fatalf("error %q does not mention describing AMIs", err.Error())
	}
}

func TestResolveUbuntuAMI_InterfaceCheck(t *testing.T) {
	var _ fabricac.AMIResolver = (*amiResolver)(nil)
}

func TestAMIResolverEnsureClient_LoadError(t *testing.T) {
	resolver := &amiResolver{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			return aws.Config{}, errors.New("no credentials")
		},
	}

	_, err := resolver.ResolveUbuntuAMI(context.Background(), "us-east-1")
	if err == nil {
		t.Fatal("expected error on config load failure")
	}
	if !containsString(err.Error(), "loading AWS config for AMI resolver") {
		t.Fatalf("error %q does not mention config load", err.Error())
	}
}

func TestNewAMIResolver(t *testing.T) {
	cfg := awsConfig{region: "eu-west-1", profile: "test"}
	r := newAMIResolver(cfg)
	if r == nil {
		t.Fatal("expected non-nil resolver")
	}
	if r.awsCfg.region != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1", r.awsCfg.region)
	}
}

func TestAMIResolverEnsureClient_Caches(t *testing.T) {
	fake := &fakeEC2ImagesClient{images: []types.Image{
		{ImageId: aws.String("ami-cached"), CreationDate: aws.String("2025-06-01T00:00:00Z")},
	}}
	loadCalls := 0
	m := &amiResolver{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			loadCalls++
			return aws.Config{}, nil
		},
		newClient: func(_ aws.Config) ec2APIClient { return fake },
	}

	// Two calls should construct the client exactly once.
	for i := 0; i < 2; i++ {
		_, err := m.ResolveUbuntuAMI(context.Background(), "us-east-1")
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if loadCalls != 1 {
		t.Errorf("loadCfg called %d times, want 1 (client should be cached)", loadCalls)
	}
}

func containsString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
