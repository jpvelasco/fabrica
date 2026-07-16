package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.EC2InstanceManager = (*ec2Manager)(nil)

type ec2Manager struct {
	awsCfg awsConfig
	client ec2APIClient

	// seams for testing — nil means use real SDK
	loadCfg   func(ctx context.Context, region, profile string) (aws.Config, error)
	newClient func(aws.Config) ec2APIClient
}

// ec2APIClient is the subset of the EC2 SDK client surface used by ec2Manager.
type ec2APIClient interface {
	StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
	StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

func (m *ec2Manager) ensureClient(ctx context.Context) error {
	if m.client != nil {
		return nil
	}
	loadCfg := m.loadCfg
	if loadCfg == nil {
		loadCfg = loadAWSConfig
	}
	cfg, err := loadCfg(ctx, m.awsCfg.region, m.awsCfg.profile)
	if err != nil {
		return fmt.Errorf("loading AWS config for EC2 manager: %w", err)
	}
	newClient := m.newClient
	if newClient == nil {
		newClient = func(cfg aws.Config) ec2APIClient {
			return ec2.NewFromConfig(cfg)
		}
	}
	m.client = newClient(cfg)
	return nil
}

// StopInstance stops the EC2 instance with the given ID and returns once the
// request is accepted (does not wait for the instance to reach stopped state).
func (m *ec2Manager) StopInstance(ctx context.Context, instanceID string) error {
	if err := m.ensureClient(ctx); err != nil {
		return err
	}
	_, err := m.client.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("stopping instance %s: %w", instanceID, err)
	}
	return nil
}

// StartInstance starts the EC2 instance with the given ID and returns once the
// request is accepted (does not wait for the instance to reach running state).
func (m *ec2Manager) StartInstance(ctx context.Context, instanceID string) error {
	if err := m.ensureClient(ctx); err != nil {
		return err
	}
	_, err := m.client.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("starting instance %s: %w", instanceID, err)
	}
	return nil
}
