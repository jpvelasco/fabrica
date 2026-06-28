package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.GameLiftManager = (*awsProvider)(nil)

// gameLiftClient is the subset of the GameLift SDK the provider uses.
type gameLiftClient interface {
	DescribeFleetAttributes(context.Context, *gamelift.DescribeFleetAttributesInput, ...func(*gamelift.Options)) (*gamelift.DescribeFleetAttributesOutput, error)
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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := p.gameLiftClient(cfg).DescribeFleetAttributes(ctx, &gamelift.DescribeFleetAttributesInput{
		FleetIds: []string{fleetID},
	})
	if err != nil {
		return fabricac.FleetInfo{}, fmt.Errorf("describing fleet %s: %w", fleetID, err)
	}
	if len(out.FleetAttributes) == 0 {
		return fabricac.FleetInfo{}, fmt.Errorf("fleet %s not found — check 'fabrica deploy status'", fleetID)
	}
	a := out.FleetAttributes[0]
	return fabricac.FleetInfo{FleetID: aws.ToString(a.FleetId), Status: string(a.Status)}, nil
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
