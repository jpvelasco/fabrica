package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/jpvelasco/fabrica/internal/assert"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

// fakeNetworkClient serves the BPA and routing describes. errs injects a
// failure per EC2 operation name; exclPages are served in call order and every
// exclusions input is recorded.
type fakeNetworkClient struct {
	stubEC2
	errs      map[string]error
	opts      *types.VpcBlockPublicAccessOptions
	exclPages []*ec2.DescribeVpcBlockPublicAccessExclusionsOutput
	exclCalls []*ec2.DescribeVpcBlockPublicAccessExclusionsInput
	subnets   []types.Subnet
	explicit  []types.RouteTable
	main      []types.RouteTable
	endpoints []types.VpcEndpoint
}

func (f *fakeNetworkClient) fail(op string) error { return f.errs[op] }

func (f *fakeNetworkClient) DescribeVpcBlockPublicAccessOptions(context.Context, *ec2.DescribeVpcBlockPublicAccessOptionsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcBlockPublicAccessOptionsOutput, error) {
	if err := f.fail("opts"); err != nil {
		return nil, err
	}
	return &ec2.DescribeVpcBlockPublicAccessOptionsOutput{VpcBlockPublicAccessOptions: f.opts}, nil
}

func (f *fakeNetworkClient) DescribeVpcBlockPublicAccessExclusions(_ context.Context, in *ec2.DescribeVpcBlockPublicAccessExclusionsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcBlockPublicAccessExclusionsOutput, error) {
	f.exclCalls = append(f.exclCalls, in)
	if err := f.fail("excl"); err != nil {
		return nil, err
	}
	if i := len(f.exclCalls) - 1; i < len(f.exclPages) {
		return f.exclPages[i], nil
	}
	return &ec2.DescribeVpcBlockPublicAccessExclusionsOutput{}, nil
}

func (f *fakeNetworkClient) DescribeSubnets(context.Context, *ec2.DescribeSubnetsInput, ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	if err := f.fail("subnet"); err != nil {
		return nil, err
	}
	return &ec2.DescribeSubnetsOutput{Subnets: f.subnets}, nil
}

func (f *fakeNetworkClient) DescribeRouteTables(_ context.Context, in *ec2.DescribeRouteTablesInput, _ ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
	if err := f.fail("routes"); err != nil {
		return nil, err
	}
	if aws.ToString(in.Filters[0].Name) == "association.subnet-id" {
		return &ec2.DescribeRouteTablesOutput{RouteTables: f.explicit}, nil
	}
	return &ec2.DescribeRouteTablesOutput{RouteTables: f.main}, nil
}

func (f *fakeNetworkClient) DescribeVpcEndpoints(context.Context, *ec2.DescribeVpcEndpointsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error) {
	if err := f.fail("endpoints"); err != nil {
		return nil, err
	}
	return &ec2.DescribeVpcEndpointsOutput{VpcEndpoints: f.endpoints}, nil
}

func exclusion(arn string, mode types.InternetGatewayExclusionMode, state types.VpcBlockPublicAccessExclusionState) types.VpcBlockPublicAccessExclusion {
	return types.VpcBlockPublicAccessExclusion{ResourceArn: aws.String(arn), InternetGatewayExclusionMode: mode, State: state}
}

func TestBlockPublicAccess_MapsModeStateAndExclusions(t *testing.T) {
	fake := &fakeNetworkClient{
		opts: &types.VpcBlockPublicAccessOptions{InternetGatewayBlockMode: types.InternetGatewayBlockModeBlockIngress, State: types.VpcBlockPublicAccessStateUpdateComplete},
		exclPages: []*ec2.DescribeVpcBlockPublicAccessExclusionsOutput{
			{
				VpcBlockPublicAccessExclusions: []types.VpcBlockPublicAccessExclusion{
					exclusion("arn:aws:ec2:us-west-2:111:subnet/subnet-a", types.InternetGatewayExclusionModeAllowBidirectional, types.VpcBlockPublicAccessExclusionStateCreateComplete),
					exclusion("arn:aws:ec2:us-west-2:111:subnet/subnet-gone", types.InternetGatewayExclusionModeAllowBidirectional, types.VpcBlockPublicAccessExclusionStateDeleteComplete),
				},
				NextToken: aws.String("page-2"),
			},
			{VpcBlockPublicAccessExclusions: []types.VpcBlockPublicAccessExclusion{
				exclusion("arn:aws:ec2:us-west-2:111:vpc/vpc-1", types.InternetGatewayExclusionModeAllowEgress, types.VpcBlockPublicAccessExclusionStateUpdateComplete),
			}},
		},
	}
	status, err := (&awsProvider{ec2: ec2Service{client: fake}}).BlockPublicAccess(context.Background())
	if err != nil {
		t.Fatalf("BlockPublicAccess: %v", err)
	}
	if status.Mode != fabricac.BPAModeBlockIngress || status.State != "update-complete" || !status.On() {
		t.Fatalf("status = %+v, want block-ingress/update-complete", status)
	}
	want := []fabricac.BPAExclusion{
		{ResourceID: "subnet-a", Mode: fabricac.BPAExclusionAllowBidirectional},
		{ResourceID: "vpc-1", Mode: fabricac.BPAExclusionAllowEgress},
	}
	if len(status.Exclusions) != len(want) || status.Exclusions[0] != want[0] || status.Exclusions[1] != want[1] {
		t.Fatalf("exclusions = %+v, want %+v", status.Exclusions, want)
	}
	// Every page must carry MaxResults, and the second call must follow the token.
	if len(fake.exclCalls) != 2 || aws.ToString(fake.exclCalls[1].NextToken) != "page-2" {
		t.Fatalf("exclusion calls = %d, want 2 following NextToken", len(fake.exclCalls))
	}
	for i, in := range fake.exclCalls {
		if aws.ToInt32(in.MaxResults) != bpaExclusionPageSize {
			t.Errorf("call %d MaxResults = %d, want %d", i, aws.ToInt32(in.MaxResults), bpaExclusionPageSize)
		}
	}
	// No options at all reads as off.
	if off, err := (&ec2Service{client: &fakeNetworkClient{}}).BlockPublicAccess(context.Background()); err != nil || off.On() {
		t.Fatalf("no options: status = %+v, err = %v; want off", off, err)
	}
}

func TestBlockPublicAccess_Errors(t *testing.T) {
	tests := []struct {
		name       string
		op         string
		err        error
		wantDenied bool
		wantText   string
	}{
		{"options denied", "opts", apiErr("UnauthorizedOperation"), true, "describing VPC Block Public Access options"},
		{"options other", "opts", errors.New("throttled"), false, "describing VPC Block Public Access options"},
		{"exclusions denied", "excl", apiErr("AccessDenied"), true, "describing VPC Block Public Access exclusions"},
		{"exclusions other", "excl", errors.New("boom"), false, "describing VPC Block Public Access exclusions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&ec2Service{client: &fakeNetworkClient{errs: map[string]error{tt.op: tt.err}}}).BlockPublicAccess(context.Background())
			assertInspectionErr(t, err, tt.wantDenied, tt.wantText)
		})
	}
}

// subnetFake returns a fake with subnet-a in vpc-1 and the given route tables.
func subnetFake(explicit, main []types.RouteTable) *fakeNetworkClient {
	return &fakeNetworkClient{
		subnets:  []types.Subnet{{SubnetId: aws.String("subnet-a"), VpcId: aws.String("vpc-1")}},
		explicit: explicit,
		main:     main,
	}
}

// defaultRouteTable returns a table whose 0.0.0.0/0 route is route.
func defaultRouteTable(id string, route types.Route) types.RouteTable {
	route.DestinationCidrBlock = aws.String("0.0.0.0/0")
	return types.RouteTable{RouteTableId: aws.String(id), Routes: []types.Route{route}}
}

func TestSubnetEgress_DefaultRouteKinds(t *testing.T) {
	tests := []struct {
		name  string
		route types.Route
		want  fabricac.DefaultRouteKind
	}{
		{"igw", types.Route{GatewayId: aws.String("igw-1")}, fabricac.RouteIGW},
		{"nat", types.Route{NatGatewayId: aws.String("nat-1")}, fabricac.RouteNAT},
		{"transit gateway", types.Route{TransitGatewayId: aws.String("tgw-1")}, fabricac.RoutePrivate},
		{"eni", types.Route{NetworkInterfaceId: aws.String("eni-1")}, fabricac.RoutePrivate},
		{"blackhole ignored", types.Route{GatewayId: aws.String("igw-1"), State: types.RouteStateBlackhole}, fabricac.RouteNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := subnetFake([]types.RouteTable{defaultRouteTable("rtb-x", tt.route)}, nil)
			got, err := (&awsProvider{ec2: ec2Service{client: fake}}).SubnetEgress(context.Background(), "subnet-a")
			if err != nil {
				t.Fatalf("SubnetEgress: %v", err)
			}
			if got.DefaultRoute != tt.want || got.RouteTableID != "rtb-x" || got.VPCID != "vpc-1" || got.SubnetID != "subnet-a" {
				t.Fatalf("got %+v, want route %s on rtb-x in vpc-1", got, tt.want)
			}
		})
	}
}

func TestSubnetEgress_MainTableAndEndpoints(t *testing.T) {
	main := []types.RouteTable{{RouteTableId: aws.String("rtb-main"), Routes: []types.Route{{DestinationCidrBlock: aws.String("10.0.0.0/16"), GatewayId: aws.String("local")}}}}
	fake := subnetFake(nil, main)
	fake.endpoints = []types.VpcEndpoint{{
		ServiceName:     aws.String("com.amazonaws.us-west-2.s3"),
		VpcEndpointType: types.VpcEndpointTypeGateway,
		State:           types.StateAvailable,
		RouteTableIds:   []string{"rtb-main"},
	}}
	got, err := (&ec2Service{client: fake}).SubnetEgress(context.Background(), "subnet-a")
	if err != nil {
		t.Fatalf("SubnetEgress: %v", err)
	}
	if got.RouteTableID != "rtb-main" || got.DefaultRoute != fabricac.RouteNone {
		t.Fatalf("got %+v, want the main table rtb-main with no default route", got)
	}
	ep := fabricac.VPCEndpoint{ServiceName: "com.amazonaws.us-west-2.s3", Type: "Gateway", State: "Available", RouteTableIDs: []string{"rtb-main"}}
	if len(got.Endpoints) != 1 || got.Endpoints[0].ServiceName != ep.ServiceName || got.Endpoints[0].Type != ep.Type ||
		got.Endpoints[0].State != ep.State || got.Endpoints[0].RouteTableIDs[0] != "rtb-main" {
		t.Fatalf("endpoints = %+v, want %+v", got.Endpoints, ep)
	}
}

func TestSubnetEgress_Errors(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*fakeNetworkClient)
		wantDenied bool
		wantText   string
	}{
		{"subnet denied", func(f *fakeNetworkClient) { f.errs = map[string]error{"subnet": apiErr("UnauthorizedOperation")} }, true, "describing subnet"},
		{"subnet missing", func(f *fakeNetworkClient) { f.subnets = nil }, false, "not found"},
		{"explicit route denied", func(f *fakeNetworkClient) { f.errs = map[string]error{"routes": apiErr("UnauthorizedOperation")} }, true, "describing route tables for subnet"},
		{"no route table", func(f *fakeNetworkClient) {}, false, "no route table found"},
		{"endpoints denied", func(f *fakeNetworkClient) {
			f.explicit = []types.RouteTable{defaultRouteTable("rtb-x", types.Route{GatewayId: aws.String("igw-1")})}
			f.errs = map[string]error{"endpoints": apiErr("AccessDenied")}
		}, true, "describing VPC endpoints in vpc-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := subnetFake(nil, nil)
			tt.mutate(fake)
			_, err := (&ec2Service{client: fake}).SubnetEgress(context.Background(), "subnet-a")
			assertInspectionErr(t, err, tt.wantDenied, tt.wantText)
		})
	}
}

// assertInspectionErr checks that err is set, names the failed action, and
// wraps cloud.ErrInspectionDenied exactly when wantDenied.
func assertInspectionErr(t *testing.T, err error, wantDenied bool, wantText string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	if got := errors.Is(err, fabricac.ErrInspectionDenied); got != wantDenied {
		t.Fatalf("errors.Is(ErrInspectionDenied) = %v, want %v (err: %v)", got, wantDenied, err)
	}
	assert.Contains(t, err.Error(), wantText)
}
