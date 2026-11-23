package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.NetworkInspector = (*ec2Service)(nil)

// bpaExclusionPageSize is sent as MaxResults on every exclusions call;
// DescribeVpcBlockPublicAccessExclusions fails with MissingParameter without it.
const bpaExclusionPageSize = 100

// BlockPublicAccess reads the region's VPC Block Public Access mode and the
// exclusions still in effect.
func (s *ec2Service) BlockPublicAccess(ctx context.Context) (fabricac.BPAStatus, error) {
	if err := s.ensureClient(ctx); err != nil {
		return fabricac.BPAStatus{}, err
	}
	opts, err := s.client.DescribeVpcBlockPublicAccessOptions(ctx, &ec2.DescribeVpcBlockPublicAccessOptionsInput{})
	if err != nil {
		return fabricac.BPAStatus{}, inspectionError("describing VPC Block Public Access options", err)
	}
	var status fabricac.BPAStatus
	if o := opts.VpcBlockPublicAccessOptions; o != nil {
		status.Mode, status.State = string(o.InternetGatewayBlockMode), string(o.State)
	}
	if status.Exclusions, err = s.bpaExclusions(ctx); err != nil {
		return fabricac.BPAStatus{}, err
	}
	return status, nil
}

// bpaExclusions pages with a manual NextToken loop; the SDK paginator omits
// MaxResults, so it cannot be used here.
func (s *ec2Service) bpaExclusions(ctx context.Context) ([]fabricac.BPAExclusion, error) {
	var out []fabricac.BPAExclusion
	var next *string
	for {
		page, err := s.client.DescribeVpcBlockPublicAccessExclusions(ctx, &ec2.DescribeVpcBlockPublicAccessExclusionsInput{
			MaxResults: aws.Int32(bpaExclusionPageSize),
			NextToken:  next,
		})
		if err != nil {
			return nil, inspectionError("describing VPC Block Public Access exclusions", err)
		}
		for _, ex := range page.VpcBlockPublicAccessExclusions {
			// Deleted and failed exclusions no longer lift the block.
			switch ex.State {
			case types.VpcBlockPublicAccessExclusionStateDeleteInProgress,
				types.VpcBlockPublicAccessExclusionStateDeleteComplete,
				types.VpcBlockPublicAccessExclusionStateCreateFailed:
				continue
			}
			out = append(out, fabricac.BPAExclusion{
				ResourceID: resourceIDFromARN(aws.ToString(ex.ResourceArn)),
				Mode:       string(ex.InternetGatewayExclusionMode),
			})
		}
		if aws.ToString(page.NextToken) == "" {
			return out, nil
		}
		next = page.NextToken
	}
}

// resourceIDFromARN returns the trailing ID of an ARN such as
// arn:aws:ec2:REGION:ACCOUNT:subnet/subnet-0abc.
func resourceIDFromARN(arn string) string {
	return arn[strings.LastIndex(arn, "/")+1:]
}

// SubnetEgress resolves the subnet's route table and the VPC endpoints in its VPC.
func (s *ec2Service) SubnetEgress(ctx context.Context, subnetID string) (fabricac.SubnetNetwork, error) {
	if err := s.ensureClient(ctx); err != nil {
		return fabricac.SubnetNetwork{}, err
	}
	subnets, err := s.client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{subnetID}})
	if err != nil {
		return fabricac.SubnetNetwork{}, inspectionError("describing subnet "+subnetID, err)
	}
	if len(subnets.Subnets) == 0 {
		return fabricac.SubnetNetwork{}, fmt.Errorf("subnet %s not found in this region; check subnetId in fabrica.yaml", subnetID)
	}
	vpcID := aws.ToString(subnets.Subnets[0].VpcId)
	rt, err := s.routeTableFor(ctx, subnetID, vpcID)
	if err != nil {
		return fabricac.SubnetNetwork{}, err
	}
	endpoints, err := s.vpcEndpoints(ctx, vpcID)
	if err != nil {
		return fabricac.SubnetNetwork{}, err
	}
	return fabricac.SubnetNetwork{
		SubnetID:     subnetID,
		VPCID:        vpcID,
		RouteTableID: aws.ToString(rt.RouteTableId),
		DefaultRoute: defaultRoute(rt),
		Endpoints:    endpoints,
	}, nil
}

// routeTableFor returns the subnet's explicit route table, else the VPC main one.
func (s *ec2Service) routeTableFor(ctx context.Context, subnetID, vpcID string) (types.RouteTable, error) {
	explicit, err := s.client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []types.Filter{{Name: aws.String("association.subnet-id"), Values: []string{subnetID}}},
	})
	if err != nil {
		return types.RouteTable{}, inspectionError("describing route tables for subnet "+subnetID, err)
	}
	if len(explicit.RouteTables) > 0 {
		return explicit.RouteTables[0], nil
	}
	main, err := s.client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
			{Name: aws.String("association.main"), Values: []string{"true"}},
		},
	})
	if err != nil {
		return types.RouteTable{}, inspectionError("describing the main route table of "+vpcID, err)
	}
	if len(main.RouteTables) == 0 {
		return types.RouteTable{}, fmt.Errorf("no route table found for subnet %s in VPC %s", subnetID, vpcID)
	}
	return main.RouteTables[0], nil
}

// defaultRoute classifies the active 0.0.0.0/0 route; blackholes carry no traffic.
func defaultRoute(rt types.RouteTable) fabricac.DefaultRouteKind {
	for _, r := range rt.Routes {
		if aws.ToString(r.DestinationCidrBlock) != "0.0.0.0/0" || r.State == types.RouteStateBlackhole {
			continue
		}
		switch {
		case strings.HasPrefix(aws.ToString(r.GatewayId), "igw-"):
			return fabricac.RouteIGW
		case aws.ToString(r.NatGatewayId) != "":
			return fabricac.RouteNAT
		default:
			return fabricac.RoutePrivate
		}
	}
	return fabricac.RouteNone
}

// vpcEndpoints lists the VPC's endpoints, following pages with the SDK paginator.
func (s *ec2Service) vpcEndpoints(ctx context.Context, vpcID string) ([]fabricac.VPCEndpoint, error) {
	pager := ec2.NewDescribeVpcEndpointsPaginator(s.client, &ec2.DescribeVpcEndpointsInput{
		Filters: []types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
	})
	var out []fabricac.VPCEndpoint
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, inspectionError("describing VPC endpoints in "+vpcID, err)
		}
		for _, e := range page.VpcEndpoints {
			out = append(out, fabricac.VPCEndpoint{
				ServiceName:   aws.ToString(e.ServiceName),
				Type:          string(e.VpcEndpointType),
				State:         string(e.State),
				RouteTableIDs: e.RouteTableIds,
			})
		}
	}
	return out, nil
}

// inspectionError wraps an EC2 describe failure; authorization failures also
// wrap cloud.ErrInspectionDenied so preflight can degrade to a warning.
func inspectionError(action string, err error) error {
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "UnauthorizedOperation" || apiErr.ErrorCode() == "AccessDenied") {
		return fmt.Errorf("%s: %w: %w", action, fabricac.ErrInspectionDenied, err)
	}
	return fmt.Errorf("%s: %w", action, err)
}
