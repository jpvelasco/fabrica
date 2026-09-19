package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestDescribeImage(t *testing.T) {
	svc := &ec2Service{
		client: &fakeEC2ImagesClient{
			images: []types.Image{{
				ImageId:     aws.String("ami-dcv"),
				Name:        aws.String("fabrica-workstation-dcv"),
				Description: aws.String("NICE DCV"),
				OwnerId:     aws.String("123"),
			}},
		},
	}
	info, err := svc.DescribeImage(context.Background(), "ami-dcv")
	if err != nil {
		t.Fatalf("DescribeImage: %v", err)
	}
	if info.ID != "ami-dcv" || info.Name != "fabrica-workstation-dcv" || info.Owner != "123" {
		t.Errorf("ImageInfo = %+v", info)
	}
}

func TestDescribeImageNotFound(t *testing.T) {
	svc := &ec2Service{
		client: &fakeEC2ImagesClient{images: nil},
	}
	_, err := svc.DescribeImage(context.Background(), "ami-missing")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDescribeImageAPIError(t *testing.T) {
	svc := &ec2Service{
		client: &fakeEC2ImagesClient{err: errors.New("denied")},
	}
	_, err := svc.DescribeImage(context.Background(), "ami-x")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDescribeImageEnsureClientError(t *testing.T) {
	svc := &ec2Service{
		loadCfg: func(context.Context, string, string) (aws.Config, error) {
			return aws.Config{}, errors.New("no creds")
		},
	}
	_, err := svc.DescribeImage(context.Background(), "ami-x")
	if err == nil {
		t.Fatal("expected ensureClient error")
	}
}

func TestAwsProviderDescribeImageDelegates(t *testing.T) {
	p := &awsProvider{
		ec2: ec2Service{
			client: &fakeEC2ImagesClient{
				images: []types.Image{{
					ImageId: aws.String("ami-1"),
					Name:    aws.String("nice-dcv-ubuntu"),
				}},
			},
		},
	}
	info, err := p.DescribeImage(context.Background(), "ami-1")
	if err != nil {
		t.Fatalf("DescribeImage: %v", err)
	}
	if info.Name != "nice-dcv-ubuntu" {
		t.Errorf("Name = %q", info.Name)
	}
}
