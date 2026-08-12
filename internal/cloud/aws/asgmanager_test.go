package aws

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

type fakeAutoScalingClient struct {
	describe func(context.Context, *autoscaling.DescribeAutoScalingGroupsInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
}

func (f *fakeAutoScalingClient) DescribeAutoScalingGroups(ctx context.Context, in *autoscaling.DescribeAutoScalingGroupsInput, o ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	return f.describe(ctx, in, o...)
}

// newTestProviderASG wires a fake Auto Scaling client and a no-op config loader.
func newTestProviderASG(c *fakeAutoScalingClient) *awsProvider {
	return &awsProvider{
		awsCfg: awsConfig{region: "us-east-1"},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{}, nil
		},
		newAutoScalingClient: func(awssdk.Config) autoScalingClient { return c },
	}
}

func TestDescribeASG_Success(t *testing.T) {
	c := &fakeAutoScalingClient{
		describe: func(_ context.Context, _ *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
			return &autoscaling.DescribeAutoScalingGroupsOutput{
				AutoScalingGroups: []types.AutoScalingGroup{
					{
						AutoScalingGroupName: awssdk.String("my-asg"),
						DesiredCapacity:      awssdk.Int32(2),
						MinSize:              awssdk.Int32(1),
						MaxSize:              awssdk.Int32(4),
						Instances: []types.Instance{
							{InstanceId: awssdk.String("i-1"), LifecycleState: types.LifecycleStateInService},
							{InstanceId: awssdk.String("i-2"), LifecycleState: types.LifecycleStateInService},
						},
					},
				},
			}, nil
		},
	}

	info, err := newTestProviderASG(c).DescribeASG(context.Background(), "my-asg")
	if err != nil {
		t.Fatalf("DescribeASG err: %v", err)
	}
	if info.Name != "my-asg" {
		t.Errorf("Name = %q, want my-asg", info.Name)
	}
	if info.DesiredCapacity != 2 {
		t.Errorf("DesiredCapacity = %d, want 2", info.DesiredCapacity)
	}
	if info.MinSize != 1 {
		t.Errorf("MinSize = %d, want 1", info.MinSize)
	}
	if info.MaxSize != 4 {
		t.Errorf("MaxSize = %d, want 4", info.MaxSize)
	}
	if info.InService != 2 {
		t.Errorf("InService = %d, want 2", info.InService)
	}
}

func TestDescribeASG_MixedLifecycleStates(t *testing.T) {
	c := &fakeAutoScalingClient{
		describe: func(_ context.Context, _ *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
			return &autoscaling.DescribeAutoScalingGroupsOutput{
				AutoScalingGroups: []types.AutoScalingGroup{
					{
						AutoScalingGroupName: awssdk.String("my-asg"),
						DesiredCapacity:      awssdk.Int32(3),
						MinSize:              awssdk.Int32(1),
						MaxSize:              awssdk.Int32(3),
						Instances: []types.Instance{
							{InstanceId: awssdk.String("i-1"), LifecycleState: types.LifecycleStateInService},
							{InstanceId: awssdk.String("i-2"), LifecycleState: types.LifecycleStatePending},
							{InstanceId: awssdk.String("i-3"), LifecycleState: types.LifecycleStateTerminating},
						},
					},
				},
			}, nil
		},
	}

	info, err := newTestProviderASG(c).DescribeASG(context.Background(), "my-asg")
	if err != nil {
		t.Fatalf("DescribeASG err: %v", err)
	}
	if info.InService != 1 {
		t.Errorf("InService = %d, want 1", info.InService)
	}
	if info.Pending != 1 {
		t.Errorf("Pending = %d, want 1", info.Pending)
	}
	if info.Terminating != 1 {
		t.Errorf("Terminating = %d, want 1", info.Terminating)
	}
}

func TestDescribeASG_NotFound(t *testing.T) {
	c := &fakeAutoScalingClient{
		describe: func(_ context.Context, _ *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
			return &autoscaling.DescribeAutoScalingGroupsOutput{}, nil
		},
	}

	_, err := newTestProviderASG(c).DescribeASG(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing ASG")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want 'not found'", err.Error())
	}
}

func TestDescribeASG_SDKError(t *testing.T) {
	c := &fakeAutoScalingClient{
		describe: func(_ context.Context, _ *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
			return nil, errors.New("access denied")
		},
	}

	_, err := newTestProviderASG(c).DescribeASG(context.Background(), "my-asg")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "describing ASG") {
		t.Fatalf("error = %q, want 'describing ASG'", err.Error())
	}
}

func TestAutoScalingClient_Default(t *testing.T) {
	p := &awsProvider{awsCfg: awsConfig{region: "us-east-1"}}
	// Without a factory set, autoScalingClient should return a real client
	// (we can't easily verify the type without importing autoscaling.NewFromConfig,
	// but we can verify it's non-nil).
	client := p.autoScalingClient(awssdk.Config{})
	if client == nil {
		t.Fatal("expected non-nil client when no factory is set")
	}
}

func TestAutoScalingClient_CustomFactory(t *testing.T) {
	fake := &fakeAutoScalingClient{}
	p := &awsProvider{
		awsCfg: awsConfig{region: "us-east-1"},
		newAutoScalingClient: func(awssdk.Config) autoScalingClient { return fake },
	}
	client := p.autoScalingClient(awssdk.Config{})
	if client != fake {
		t.Fatal("expected custom factory client to be returned")
	}
}
