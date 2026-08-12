package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.ASGManager = (*awsProvider)(nil)

// autoScalingClient is the subset of the Auto Scaling SDK the provider uses.
type autoScalingClient interface {
	DescribeAutoScalingGroups(context.Context, *autoscaling.DescribeAutoScalingGroupsInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
}

type autoScalingClientFactory func(aws.Config) autoScalingClient

func (p *awsProvider) autoScalingClient(cfg aws.Config) autoScalingClient {
	if p.newAutoScalingClient != nil {
		return p.newAutoScalingClient(cfg)
	}
	return autoscaling.NewFromConfig(cfg)
}

// DescribeASG returns the live state of an Auto Scaling Group by name.
func (p *awsProvider) DescribeASG(ctx context.Context, asgName string) (fabricac.ASGInfo, error) {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return fabricac.ASGInfo{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := p.autoScalingClient(cfg).DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{asgName},
	})
	if err != nil {
		return fabricac.ASGInfo{}, fmt.Errorf("describing ASG %s: %w", asgName, err)
	}
	if len(out.AutoScalingGroups) == 0 {
		return fabricac.ASGInfo{}, fmt.Errorf("ASG %s not found", asgName)
	}

	g := out.AutoScalingGroups[0]
	info := fabricac.ASGInfo{
		Name:            aws.ToString(g.AutoScalingGroupName),
		DesiredCapacity: int(aws.ToInt32(g.DesiredCapacity)),
		MinSize:         int(aws.ToInt32(g.MinSize)),
		MaxSize:         int(aws.ToInt32(g.MaxSize)),
	}

	// Count instances by lifecycle state.
	for _, inst := range g.Instances {
		switch inst.LifecycleState {
		case types.LifecycleStateInService:
			info.InService++
		case types.LifecycleStatePending:
			info.Pending++
		case types.LifecycleStateTerminating:
			info.Terminating++
		}
	}

	return info, nil
}
