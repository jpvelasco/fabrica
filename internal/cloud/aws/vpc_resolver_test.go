package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/jpvelasco/fabrica/internal/assert"
)

type fakeVPCClient struct {
	stubEC2
	vpcs        []types.Vpc
	subnets     []types.Subnet
	describeErr error
}

func (f *fakeVPCClient) DescribeVpcs(context.Context, *ec2.DescribeVpcsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	return &ec2.DescribeVpcsOutput{Vpcs: f.vpcs}, nil
}

func (f *fakeVPCClient) DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	return &ec2.DescribeSubnetsOutput{Subnets: f.subnets}, nil
}

func TestResolveDefaultVPC_Success(t *testing.T) {
	fake := &fakeVPCClient{
		vpcs:    []types.Vpc{{VpcId: aws.String("vpc-default")}},
		subnets: []types.Subnet{{SubnetId: aws.String("subnet-a")}, {SubnetId: aws.String("subnet-b")}},
	}
	resolver := &ec2Service{client: fake}

	vpcID, subnetID, err := resolver.ResolveDefaultVPC(context.Background())
	if err != nil {
		t.Fatalf("ResolveDefaultVPC: %v", err)
	}
	if vpcID != "vpc-default" {
		t.Errorf("vpcID = %q, want vpc-default", vpcID)
	}
	if subnetID != "subnet-a" {
		t.Errorf("subnetID = %q, want subnet-a (first)", subnetID)
	}
}

func TestResolveDefaultVPC_NoDefaultVPC(t *testing.T) {
	fake := &fakeVPCClient{vpcs: nil}
	resolver := &ec2Service{client: fake}

	_, _, err := resolver.ResolveDefaultVPC(context.Background())
	if err == nil {
		t.Fatal("expected error when no default VPC")
	}
	assert.Contains(t, err.Error(), "no default VPC found")
}

func TestResolveDefaultVPC_NoSubnets(t *testing.T) {
	fake := &fakeVPCClient{vpcs: []types.Vpc{{VpcId: aws.String("vpc-default")}}, subnets: nil}
	resolver := &ec2Service{client: fake}

	_, _, err := resolver.ResolveDefaultVPC(context.Background())
	if err == nil {
		t.Fatal("expected error when VPC has no subnets")
	}
	assert.Contains(t, err.Error(), "no subnets in default VPC")
}

func TestResolveDefaultVPC_DescribeError(t *testing.T) {
	fake := &fakeVPCClient{describeErr: errors.New("access denied")}
	resolver := &ec2Service{client: fake}

	_, _, err := resolver.ResolveDefaultVPC(context.Background())
	if err == nil {
		t.Fatal("expected error on describe failure")
	}
	assert.Contains(t, err.Error(), "describing default VPC")
}

func TestResolveDefaultVPC_LoadConfigError(t *testing.T) {
	s := &ec2Service{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(context.Context, string, string) (aws.Config, error) {
			return aws.Config{}, errors.New("no credentials")
		},
	}
	_, _, err := s.ResolveDefaultVPC(context.Background())
	if err == nil {
		t.Fatal("expected error when config load fails")
	}
	assert.Contains(t, err.Error(), "loading AWS config for EC2 service")
}

func TestAwsProviderResolveDefaultVPC(t *testing.T) {
	fake := &fakeVPCClient{vpcs: []types.Vpc{{VpcId: aws.String("vpc-default")}}, subnets: []types.Subnet{{SubnetId: aws.String("subnet-a")}}}
	p := &awsProvider{
		ec2: ec2Service{client: fake},
	}
	vpcID, subnetID, err := p.ResolveDefaultVPC(context.Background())
	if err != nil {
		t.Fatalf("ResolveDefaultVPC: %v", err)
	}
	if vpcID != "vpc-default" || subnetID != "subnet-a" {
		t.Errorf("got %q/%q, want vpc-default/subnet-a", vpcID, subnetID)
	}
}
