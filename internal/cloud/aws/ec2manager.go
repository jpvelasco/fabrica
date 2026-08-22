package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.EC2InstanceManager = (*ec2Service)(nil)

// ec2Service owns the provider's lazily initialized EC2 SDK client. Both
// instance lifecycle actions and AMI resolution share this single client.
type ec2Service struct {
	awsCfg awsConfig
	client ec2APIClient

	// initMu serializes lazy initialization (concurrent MCP tool calls share
	// this service). Not held during API calls.
	initMu sync.Mutex

	// seams for testing — nil means use real SDK
	loadCfg   func(ctx context.Context, region, profile string) (aws.Config, error)
	newClient func(aws.Config) ec2APIClient
}

// ec2APIClient is the subset of the EC2 SDK client surface used by ec2Service.
type ec2APIClient interface {
	StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
	StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
	DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
	DescribeVpcs(ctx context.Context, params *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
}

func (s *ec2Service) ensureClient(ctx context.Context) error {
	s.initMu.Lock()
	defer s.initMu.Unlock()

	if s.client != nil {
		return nil
	}
	loadCfg := s.loadCfg
	if loadCfg == nil {
		loadCfg = loadAWSConfig
	}
	cfg, err := loadCfg(ctx, s.awsCfg.region, s.awsCfg.profile)
	if err != nil {
		return fmt.Errorf("loading AWS config for EC2 service: %w", err)
	}
	newClient := s.newClient
	if newClient == nil {
		newClient = func(cfg aws.Config) ec2APIClient {
			return ec2.NewFromConfig(cfg)
		}
	}
	s.client = newClient(cfg)
	return nil
}

// StopInstance stops the EC2 instance with the given ID and returns once the
// request is accepted (does not wait for the instance to reach stopped state).
func (s *ec2Service) StopInstance(ctx context.Context, instanceID string) error {
	if err := s.ensureClient(ctx); err != nil {
		return err
	}
	_, err := s.client.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("stopping instance %s: %w", instanceID, err)
	}
	return nil
}

// StartInstance starts the EC2 instance with the given ID and returns once the
// request is accepted (does not wait for the instance to reach running state).
func (s *ec2Service) StartInstance(ctx context.Context, instanceID string) error {
	if err := s.ensureClient(ctx); err != nil {
		return err
	}
	_, err := s.client.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("starting instance %s: %w", instanceID, err)
	}
	return nil
}
