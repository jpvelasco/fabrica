package aws

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	gltypes "github.com/aws/aws-sdk-go-v2/service/gamelift/types"
)

type fakeGameLiftClient struct {
	describeAttrs  func(context.Context, *gamelift.DescribeFleetAttributesInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error)
	describeEvents func(context.Context, *gamelift.DescribeFleetEventsInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error)
}

func (f fakeGameLiftClient) DescribeFleetAttributes(ctx context.Context, in *gamelift.DescribeFleetAttributesInput, o ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
	return f.describeAttrs(ctx, in, o...)
}
func (f fakeGameLiftClient) DescribeFleetEvents(ctx context.Context, in *gamelift.DescribeFleetEventsInput, o ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error) {
	return f.describeEvents(ctx, in, o...)
}

// newTestProviderGL wires a fake GameLift client and a no-op config loader.
// The config-loader field name matches the one stateBackendConfig uses: loadConfig.
func newTestProviderGL(c gameLiftClient) *awsProvider {
	return &awsProvider{
		awsCfg: awsConfig{region: "us-east-1"},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{}, nil
		},
		newGameLiftClient: func(awssdk.Config) gameLiftClient { return c },
	}
}

func TestFleetStatus(t *testing.T) {
	c := fakeGameLiftClient{
		describeAttrs: func(_ context.Context, _ *gamelift.DescribeFleetAttributesInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
			return &gamelift.DescribeFleetAttributesOutput{
				FleetAttributes: []gltypes.FleetAttributes{{
					FleetId: awssdk.String("fleet-1"),
					Status:  gltypes.FleetStatusActive,
				}},
			}, nil
		},
	}
	info, err := newTestProviderGL(c).FleetStatus(context.Background(), "fleet-1")
	if err != nil {
		t.Fatalf("FleetStatus err: %v", err)
	}
	if info.Status != "ACTIVE" || info.FleetID != "fleet-1" {
		t.Fatalf("got %+v", info)
	}
}

func TestFleetStatusNotFound(t *testing.T) {
	c := fakeGameLiftClient{
		describeAttrs: func(_ context.Context, _ *gamelift.DescribeFleetAttributesInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
			return &gamelift.DescribeFleetAttributesOutput{FleetAttributes: nil}, nil
		},
	}
	_, err := newTestProviderGL(c).FleetStatus(context.Background(), "fleet-x")
	if err == nil {
		t.Fatal("expected error for missing fleet")
	}
}

func TestFleetEvents(t *testing.T) {
	c := fakeGameLiftClient{
		describeEvents: func(_ context.Context, _ *gamelift.DescribeFleetEventsInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error) {
			return &gamelift.DescribeFleetEventsOutput{
				Events: []gltypes.Event{{
					EventCode: gltypes.EventCodeFleetStateError,
					Message:   awssdk.String("bad launch path"),
				}},
			}, nil
		},
	}
	evs, err := newTestProviderGL(c).FleetEvents(context.Background(), "fleet-1")
	if err != nil {
		t.Fatalf("FleetEvents err: %v", err)
	}
	if len(evs) != 1 || evs[0].Message != "bad launch path" {
		t.Fatalf("got %+v", evs)
	}
}

func TestFleetEventsAPIError(t *testing.T) {
	c := fakeGameLiftClient{
		describeEvents: func(_ context.Context, _ *gamelift.DescribeFleetEventsInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error) {
			return nil, errors.New("throttled")
		},
	}
	_, err := newTestProviderGL(c).FleetEvents(context.Background(), "fleet-1")
	if err == nil {
		t.Fatal("expected error propagated")
	}
}
