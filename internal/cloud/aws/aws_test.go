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
	if ap.ec2.awsCfg != ap.awsCfg {
		t.Errorf("EC2 config = %+v, want %+v", ap.ec2.awsCfg, ap.awsCfg)
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

func TestAwsProviderWithRegion(t *testing.T) {
	p, err := newProvider(config.Defaults())
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	ap := p.(*awsProvider)
	view, err := ap.WithRegion(context.Background(), "eu-west-1")
	if err != nil {
		t.Fatalf("WithRegion: %v", err)
	}
	if view.Resources == nil {
		t.Fatal("view.Resources is nil")
	}
	if view.VPCs == nil {
		t.Fatal("view.VPCs is nil")
	}
	rc, ok := view.Resources.(*resourceClients)
	if !ok {
		t.Fatalf("Resources type = %T, want *resourceClients", view.Resources)
	}
	if rc.awsCfg.region != "eu-west-1" {
		t.Fatalf("scoped client region = %q, want eu-west-1", rc.awsCfg.region)
	}
	if rc.awsCfg.profile != ap.awsCfg.profile {
		t.Fatalf("scoped client profile = %q, want %q", rc.awsCfg.profile, ap.awsCfg.profile)
	}
	if ap.awsCfg.region != config.DefaultAWSRegion {
		t.Fatalf("WithRegion mutated the provider: region = %q", ap.awsCfg.region)
	}
	// VPCs must resolve in the target region too.
	vp, ok := view.VPCs.(*awsProvider)
	if !ok {
		t.Fatalf("VPCs type = %T, want *awsProvider", view.VPCs)
	}
	if vp.awsCfg.region != "eu-west-1" {
		t.Fatalf("scoped VPC resolver region = %q, want eu-west-1", vp.awsCfg.region)
	}
}

func TestAwsProviderWithRegionEmpty(t *testing.T) {
	p := &awsProvider{awsCfg: awsConfig{region: "us-east-1"}}
	if _, err := p.WithRegion(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty region")
	}
}

func TestAwsProviderStopStart(t *testing.T) {
	p := &awsProvider{
		ec2: ec2Service{
			awsCfg: awsConfig{region: "us-east-1"},
			loadCfg: func(context.Context, string, string) (aws.Config, error) {
				return aws.Config{Region: "us-east-1"}, nil
			},
			newClient: func(aws.Config) ec2APIClient { return &stubEC2{} },
		},
	}
	if err := p.StopInstance(context.Background(), "i-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.StartInstance(context.Background(), "i-1"); err != nil {
		t.Fatal(err)
	}
}

func TestAwsProviderResolveUbuntuAMI(t *testing.T) {
	fake := &stubEC2{}
	p := &awsProvider{
		ec2: ec2Service{
			awsCfg:    awsConfig{region: "us-east-1"},
			client:    fake,
			loadCfg:   func(context.Context, string, string) (aws.Config, error) { return aws.Config{}, nil },
			newClient: func(aws.Config) ec2APIClient { return fake },
		},
	}

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
func (stubEC2) DescribeImages(context.Context, *ec2.DescribeImagesInput, ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	return &ec2.DescribeImagesOutput{}, nil
}
func (stubEC2) DescribeVpcs(context.Context, *ec2.DescribeVpcsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return &ec2.DescribeVpcsOutput{}, nil
}
func (stubEC2) DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return &ec2.DescribeSubnetsOutput{}, nil
}
