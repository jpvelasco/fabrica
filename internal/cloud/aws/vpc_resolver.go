package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.VPCResolver = (*ec2Service)(nil)

// ResolveDefaultVPC returns the account's default VPC and one of its subnets.
// It looks up the default VPC by the is-default filter, then returns the first
// subnet in that VPC. Used to give VPC-bound modules (perforce, horde, ci, …)
// a sane networking fallback when no explicit vpcId/subnetId is configured.
func (s *ec2Service) ResolveDefaultVPC(ctx context.Context) (string, string, error) {
	if err := s.ensureClient(ctx); err != nil {
		return "", "", err
	}

	vpcs, err := s.client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		Filters: []types.Filter{
			{Name: aws.String("is-default"), Values: []string{"true"}},
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("describing default VPC: %w", err)
	}
	if len(vpcs.Vpcs) == 0 {
		return "", "", fmt.Errorf("no default VPC found in this account/region — set vpcId and subnetId explicitly in fabrica.yaml")
	}
	vpcID := aws.ToString(vpcs.Vpcs[0].VpcId)

	subnets, err := s.client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("describing subnets in default VPC %s: %w", vpcID, err)
	}
	if len(subnets.Subnets) == 0 {
		return "", "", fmt.Errorf("no subnets in default VPC %s — set vpcId and subnetId explicitly in fabrica.yaml", vpcID)
	}
	return vpcID, aws.ToString(subnets.Subnets[0].SubnetId), nil
}
