package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewProvider(t *testing.T) {
	cfg := config.Defaults()
	p, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	if p.Name() != "aws" {
		t.Errorf("Name() = %q, want aws", p.Name())
	}
	ap, ok := p.(*awsProvider)
	if !ok {
		t.Fatal("expected *awsProvider")
	}
	if ap.Resources() == nil {
		t.Fatal("Resources() returned nil")
	}
}

func TestNewProviderWithProfile(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.Profile = "my-profile"
	cfg.Cloud.AWS.Region = "eu-west-1"

	p, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	ap, ok := p.(*awsProvider)
	if !ok {
		t.Fatal("expected *awsProvider")
	}
	if ap.awsCfg.profile != "my-profile" {
		t.Errorf("profile = %q, want my-profile", ap.awsCfg.profile)
	}
	if ap.awsCfg.region != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1", ap.awsCfg.region)
	}
}

func TestProviderInterface(t *testing.T) {
	cfg := config.Defaults()
	p, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}

	// Verify type compliance
	var _ interface {
		Name() string
	} = p
}

func TestAwsProviderIdentity(t *testing.T) {
	prevLoad := identityLoadCfg
	prevClient := identityNewClient
	t.Cleanup(func() {
		identityLoadCfg = prevLoad
		identityNewClient = prevClient
	})
	identityLoadCfg = func(context.Context, string, string) (aws.Config, error) {
		return aws.Config{Region: "us-east-1"}, nil
	}
	identityNewClient = func(aws.Config) stsAPIClient {
		return stubSTS{account: "123456789012", arn: "arn:aws:iam::123456789012:user/t"}
	}
	p := &awsProvider{awsCfg: awsConfig{region: "us-east-1"}}
	acct, arn, region, err := p.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if acct != "123456789012" || arn == "" || region != "us-east-1" {
		t.Fatalf("got %s %s %s", acct, arn, region)
	}
}

type stubSTS struct {
	account, arn string
}

func (s stubSTS) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{
		Account: aws.String(s.account),
		Arn:     aws.String(s.arn),
	}, nil
}

func TestAwsProviderEC2ManagerStopStart(t *testing.T) {
	p := &awsProvider{
		awsCfg: awsConfig{region: "us-east-1"},
		ec2Mgr: ec2Manager{
			awsCfg: awsConfig{region: "us-east-1"},
			loadCfg: func(context.Context, string, string) (aws.Config, error) {
				return aws.Config{Region: "us-east-1"}, nil
			},
			newClient: func(aws.Config) ec2APIClient { return &stubEC2{} },
		},
	}
	if p.EC2Manager() == nil {
		t.Fatal("EC2Manager nil")
	}
	if err := p.StopInstance(context.Background(), "i-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.StartInstance(context.Background(), "i-1"); err != nil {
		t.Fatal(err)
	}
}

func TestAwsProviderAMIResolver(t *testing.T) {
	fake := &stubEC2{}
	p := &awsProvider{
		awsCfg: awsConfig{region: "us-east-1"},
		amiRes: &amiResolver{
			awsCfg:    awsConfig{region: "us-east-1"},
			client:    fake,
			loadCfg:   func(context.Context, string, string) (aws.Config, error) { return aws.Config{}, nil },
			newClient: func(aws.Config) ec2APIClient { return fake },
		},
	}

	// AMIResolver getter should return the pre-set resolver
	resolver := p.AMIResolver()
	if resolver == nil {
		t.Fatal("AMIResolver nil")
	}

	// ResolveUbuntuAMI should delegate to the resolver
	_, err := p.ResolveUbuntuAMI(context.Background(), "us-east-1")
	if err == nil {
		t.Fatal("expected error (stub returns no images)")
	}
}

type stubEC2 struct{}

func (stubEC2) StopInstances(context.Context, *ec2.StopInstancesInput, ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	return &ec2.StopInstancesOutput{}, nil
}
func (stubEC2) StartInstances(context.Context, *ec2.StartInstancesInput, ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	return &ec2.StartInstancesOutput{}, nil
}
func (stubEC2) DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{}, nil
}
func (stubEC2) DescribeImages(context.Context, *ec2.DescribeImagesInput, ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	return &ec2.DescribeImagesOutput{}, nil
}
