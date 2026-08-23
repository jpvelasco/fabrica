package aws

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/jpvelasco/fabrica/internal/assert"
)

// fakeEC2ImagesClient implements ec2APIClient for AMI resolver tests.
type fakeEC2ImagesClient struct {
	stubEC2
	images []types.Image
	err    error
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

	resolver := &ec2Service{client: fake}
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
	resolver := &ec2Service{client: fake}

	_, err := resolver.ResolveUbuntuAMI(context.Background(), "us-east-1")
	if err == nil {
		t.Fatal("expected error when no images found")
	}
	assert.Contains(t, err.Error(), "no Ubuntu 22.04 AMI found")
}

func TestResolveUbuntuAMI_DescribeError(t *testing.T) {
	fake := &fakeEC2ImagesClient{err: errors.New("access denied")}
	resolver := &ec2Service{client: fake}

	_, err := resolver.ResolveUbuntuAMI(context.Background(), "us-east-1")
	if err == nil {
		t.Fatal("expected error on describe failure")
	}
	assert.Contains(t, err.Error(), "describing Ubuntu AMIs")
}

func (f *fakeEC2ImagesClient) DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return &ec2.DescribeVolumesOutput{}, nil
}

func (f *fakeEC2ImagesClient) CreateTags(context.Context, *ec2.CreateTagsInput, ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return &ec2.CreateTagsOutput{}, nil
}
