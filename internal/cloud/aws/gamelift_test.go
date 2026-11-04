package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	gltypes "github.com/aws/aws-sdk-go-v2/service/gamelift/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

type fakeGameLiftClient struct {
	describeAttrs    func(context.Context, *gamelift.DescribeFleetAttributesInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error)
	describeCapacity func(context.Context, *gamelift.DescribeFleetCapacityInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetCapacityOutput, error)
	describeEvents   func(context.Context, *gamelift.DescribeFleetEventsInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error)
}

func (f fakeGameLiftClient) DescribeFleetAttributes(ctx context.Context, in *gamelift.DescribeFleetAttributesInput, o ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
	return f.describeAttrs(ctx, in, o...)
}
func (f fakeGameLiftClient) DescribeFleetCapacity(ctx context.Context, in *gamelift.DescribeFleetCapacityInput, o ...func(*gamelift.Options)) (*gamelift.DescribeFleetCapacityOutput, error) {
	if f.describeCapacity == nil {
		return &gamelift.DescribeFleetCapacityOutput{}, nil
	}
	return f.describeCapacity(ctx, in, o...)
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

// TestCreateFleetAsync verifies the provider-level delegation: CreateFleetAsync
// reaches the real embedded resourceClients (not a stub) and records the
// identifier GameLift assigned.
func TestCreateFleetAsync(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{
				Identifier: awssdk.String("fleet-async"),
			},
		},
	}
	p := &awsProvider{awsCfg: awsConfig{region: "us-east-1"}}
	// resourceClients embeds a mutex and cannot be copied, so wire the fake
	// into the provider's embedded client in place.
	p.clients.cc = client
	p.clients.version = "test"
	r := &fabricac.Resource{
		TypeName:     "AWS::GameLift::Fleet",
		DesiredState: json.RawMessage(`{"Name":"test-fleet"}`),
	}
	if err := p.CreateFleetAsync(context.Background(), r); err != nil {
		t.Fatalf("CreateFleetAsync: %v", err)
	}
	if r.Identifier != "fleet-async" {
		t.Errorf("Identifier = %q, want fleet-async", r.Identifier)
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

func TestFleetStatusCapacityMapping(t *testing.T) {
	c := fakeGameLiftClient{
		describeAttrs: func(_ context.Context, _ *gamelift.DescribeFleetAttributesInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
			return &gamelift.DescribeFleetAttributesOutput{
				FleetAttributes: []gltypes.FleetAttributes{{
					FleetId: awssdk.String("fleet-1"),
					Status:  gltypes.FleetStatusActive,
				}},
			}, nil
		},
		describeCapacity: func(_ context.Context, in *gamelift.DescribeFleetCapacityInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetCapacityOutput, error) {
			if len(in.FleetIds) != 1 || in.FleetIds[0] != "fleet-1" {
				return nil, fmt.Errorf("unexpected fleet ids %v", in.FleetIds)
			}
			return &gamelift.DescribeFleetCapacityOutput{
				FleetCapacity: []gltypes.FleetCapacity{{
					FleetId: awssdk.String("fleet-1"),
					InstanceCounts: &gltypes.EC2InstanceCounts{
						ACTIVE:  awssdk.Int32(2),
						DESIRED: awssdk.Int32(2),
						MINIMUM: awssdk.Int32(0),
						MAXIMUM: awssdk.Int32(3),
					},
				}},
			}, nil
		},
	}
	info, err := newTestProviderGL(c).FleetStatus(context.Background(), "fleet-1")
	if err != nil {
		t.Fatalf("FleetStatus err: %v", err)
	}
	if info.CapacityCurrent != 2 || info.CapacityDesired != 2 || info.CapacityMinimum != 0 || info.CapacityMaximum != 3 {
		t.Fatalf("capacity = %+v, want 2/2/0/3", info)
	}
}

func TestFleetStatusCapacityMatchesFleetID(t *testing.T) {
	c := fakeGameLiftClient{
		describeAttrs: func(_ context.Context, _ *gamelift.DescribeFleetAttributesInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
			return &gamelift.DescribeFleetAttributesOutput{
				FleetAttributes: []gltypes.FleetAttributes{{
					FleetId: awssdk.String("fleet-b"),
					Status:  gltypes.FleetStatusActive,
				}},
			}, nil
		},
		describeCapacity: func(_ context.Context, _ *gamelift.DescribeFleetCapacityInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetCapacityOutput, error) {
			return &gamelift.DescribeFleetCapacityOutput{
				FleetCapacity: []gltypes.FleetCapacity{
					{
						FleetId: awssdk.String("fleet-a"),
						InstanceCounts: &gltypes.EC2InstanceCounts{
							ACTIVE: awssdk.Int32(9), DESIRED: awssdk.Int32(9),
						},
					},
					{
						FleetId: awssdk.String("fleet-b"),
						InstanceCounts: &gltypes.EC2InstanceCounts{
							ACTIVE: awssdk.Int32(4), DESIRED: awssdk.Int32(4),
						},
					},
				},
			}, nil
		},
	}
	info, err := newTestProviderGL(c).FleetStatus(context.Background(), "fleet-b")
	if err != nil {
		t.Fatalf("FleetStatus err: %v", err)
	}
	if info.CapacityCurrent != 4 || info.CapacityDesired != 4 {
		t.Fatalf("capacity = %+v, want fleet-b counts 4/4", info)
	}
}

func TestFleetStatusCapacityErrorIsBestEffort(t *testing.T) {
	c := fakeGameLiftClient{
		describeAttrs: func(_ context.Context, _ *gamelift.DescribeFleetAttributesInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
			return &gamelift.DescribeFleetAttributesOutput{
				FleetAttributes: []gltypes.FleetAttributes{{
					FleetId: awssdk.String("fleet-1"),
					Status:  gltypes.FleetStatusActive,
				}},
			}, nil
		},
		describeCapacity: func(_ context.Context, _ *gamelift.DescribeFleetCapacityInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetCapacityOutput, error) {
			return nil, errors.New("throttled")
		},
	}
	info, err := newTestProviderGL(c).FleetStatus(context.Background(), "fleet-1")
	if err != nil {
		t.Fatalf("capacity hiccup must not fail FleetStatus: %v", err)
	}
	if info.CapacityCurrent != 0 || info.CapacityDesired != 0 {
		t.Fatalf("capacity = %+v, want zeros after a failed capacity call", info)
	}
}

func TestFleetStatusSkipsCapacityBeforeActive(t *testing.T) {
	called := false
	c := fakeGameLiftClient{
		describeAttrs: func(_ context.Context, _ *gamelift.DescribeFleetAttributesInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
			return &gamelift.DescribeFleetAttributesOutput{
				FleetAttributes: []gltypes.FleetAttributes{{
					FleetId: awssdk.String("fleet-1"),
					Status:  gltypes.FleetStatusActivating,
				}},
			}, nil
		},
		describeCapacity: func(_ context.Context, _ *gamelift.DescribeFleetCapacityInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetCapacityOutput, error) {
			called = true
			return nil, errors.New("should not be called before ACTIVE")
		},
	}
	info, err := newTestProviderGL(c).FleetStatus(context.Background(), "fleet-1")
	if err != nil {
		t.Fatalf("FleetStatus err: %v", err)
	}
	if called {
		t.Fatal("DescribeFleetCapacity must not be called before the fleet is ACTIVE")
	}
	if info.Status != "ACTIVATING" || info.CapacityCurrent != 0 {
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

func TestFleetStatusSDKError(t *testing.T) {
	c := fakeGameLiftClient{
		describeAttrs: func(_ context.Context, _ *gamelift.DescribeFleetAttributesInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error) {
			return nil, errors.New("connection refused")
		},
	}
	_, err := newTestProviderGL(c).FleetStatus(context.Background(), "fleet-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "describing fleet") {
		t.Fatalf("error = %q, want substring %q", got, "describing fleet")
	}
}

func TestFleetEventsWithEventTime(t *testing.T) {
	ts := awssdk.Time(time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC))
	c := fakeGameLiftClient{
		describeEvents: func(_ context.Context, _ *gamelift.DescribeFleetEventsInput, _ ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error) {
			return &gamelift.DescribeFleetEventsOutput{
				Events: []gltypes.Event{{
					EventCode: gltypes.EventCodeFleetStateActive,
					Message:   awssdk.String("fleet ready"),
					EventTime: ts,
				}},
			}, nil
		},
	}
	evs, err := newTestProviderGL(c).FleetEvents(context.Background(), "fleet-1")
	if err != nil {
		t.Fatalf("FleetEvents err: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	if evs[0].Time != "2026-07-15T10:30:00Z" {
		t.Fatalf("ev.Time = %q, want RFC3339 formatted time", evs[0].Time)
	}
}
