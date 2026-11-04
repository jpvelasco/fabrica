package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	gltypes "github.com/aws/aws-sdk-go-v2/service/gamelift/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.GameLiftManager = (*awsProvider)(nil)

// gameLiftClient is the subset of the GameLift SDK the provider uses.
type gameLiftClient interface {
	DescribeFleetAttributes(context.Context, *gamelift.DescribeFleetAttributesInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error)
	DescribeFleetCapacity(context.Context, *gamelift.DescribeFleetCapacityInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetCapacityOutput, error)
	DescribeFleetEvents(context.Context, *gamelift.DescribeFleetEventsInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetEventsOutput, error)
}

type gameLiftClientFactory func(aws.Config) gameLiftClient

func (p *awsProvider) gameLiftClient(cfg aws.Config) gameLiftClient {
	if p.newGameLiftClient != nil {
		return p.newGameLiftClient(cfg)
	}
	return gamelift.NewFromConfig(cfg)
}

// CreateFleetAsync creates the fleet via Cloud Control but returns as soon as the
// FleetId is assigned, without blocking until ACTIVE. The cmd layer polls
// FleetStatus to track activation.
func (p *awsProvider) CreateFleetAsync(ctx context.Context, r *fabricac.Resource) error {
	return p.Resources().(*resourceClients).createAsync(ctx, r)
}

func (p *awsProvider) FleetStatus(ctx context.Context, fleetID string) (fabricac.FleetInfo, error) {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return fabricac.FleetInfo{}, err
	}
	client := p.gameLiftClient(cfg)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := client.DescribeFleetAttributes(ctx, &gamelift.DescribeFleetAttributesInput{
		FleetIds: []string{fleetID},
	})
	if err != nil {
		return fabricac.FleetInfo{}, fmt.Errorf("describing fleet %s: %w", fleetID, err)
	}
	if len(out.FleetAttributes) == 0 {
		return fabricac.FleetInfo{}, fmt.Errorf("fleet %s not found — check 'fabrica deploy status'", fleetID)
	}
	a := out.FleetAttributes[0]
	info := fabricac.FleetInfo{FleetID: aws.ToString(a.FleetId), Status: string(a.Status)}
	// Capacity counts only matter once the fleet is ACTIVE, when the promote
	// poll reconciles them before accepting the fleet; skip the extra call
	// in every earlier phase.
	if a.Status == gltypes.FleetStatusActive {
		info.CapacityCurrent, info.CapacityDesired, info.CapacityMinimum, info.CapacityMaximum =
			p.fleetCapacityCounts(ctx, client, fleetID)
	}
	return info, nil
}

// fleetCapacityCounts best-effort fetches the EC2 instance counts GameLift
// reports for a fleet (DescribeFleetCapacity). Zero counts when the call
// fails or the fleet has no capacity entry: a capacity hiccup must not kill
// the activation poll, and callers treat zero counts as "not exposed".
func (p *awsProvider) fleetCapacityCounts(ctx context.Context, client gameLiftClient, fleetID string) (current, desired, minimum, maximum int) {
	out, err := client.DescribeFleetCapacity(ctx, &gamelift.DescribeFleetCapacityInput{
		FleetIds: []string{fleetID},
	})
	if err != nil {
		return 0, 0, 0, 0
	}
	for _, c := range out.FleetCapacity {
		if aws.ToString(c.FleetId) != fleetID || c.InstanceCounts == nil {
			continue
		}
		return int(aws.ToInt32(c.InstanceCounts.ACTIVE)),
			int(aws.ToInt32(c.InstanceCounts.DESIRED)),
			int(aws.ToInt32(c.InstanceCounts.MINIMUM)),
			int(aws.ToInt32(c.InstanceCounts.MAXIMUM))
	}
	return 0, 0, 0, 0
}

func (p *awsProvider) FleetEvents(ctx context.Context, fleetID string) ([]fabricac.FleetEvent, error) {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := p.gameLiftClient(cfg).DescribeFleetEvents(ctx, &gamelift.DescribeFleetEventsInput{
		FleetId: aws.String(fleetID),
		Limit:   aws.Int32(20),
	})
	if err != nil {
		return nil, fmt.Errorf("describing events for fleet %s: %w", fleetID, err)
	}
	events := make([]fabricac.FleetEvent, 0, len(out.Events))
	for _, e := range out.Events {
		ev := fabricac.FleetEvent{Code: string(e.EventCode), Message: aws.ToString(e.Message)}
		if e.EventTime != nil {
			ev.Time = e.EventTime.Format(time.RFC3339)
		}
		events = append(events, ev)
	}
	return events, nil
}
