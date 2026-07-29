package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// fakeEC2Client implements ec2APIClient for testing.
type fakeEC2Client struct {
	stubEC2
	stopErr     error
	startErr    error
	stopCalls   int
	startCalls  int
	lastStopIDs []string
	lastStartID []string
}

func (f *fakeEC2Client) StopInstances(_ context.Context, in *ec2.StopInstancesInput, _ ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	f.stopCalls++
	f.lastStopIDs = in.InstanceIds
	if f.stopErr != nil {
		return nil, f.stopErr
	}
	return &ec2.StopInstancesOutput{}, nil
}

func (f *fakeEC2Client) StartInstances(_ context.Context, in *ec2.StartInstancesInput, _ ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	f.startCalls++
	f.lastStartID = in.InstanceIds
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &ec2.StartInstancesOutput{}, nil
}

func TestStopInstance_Success(t *testing.T) {
	fake := &fakeEC2Client{}
	m := &ec2Service{client: fake}

	if err := m.StopInstance(context.Background(), "i-abc123"); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	if fake.stopCalls != 1 {
		t.Errorf("StopInstances called %d times, want 1", fake.stopCalls)
	}
	if len(fake.lastStopIDs) != 1 || fake.lastStopIDs[0] != "i-abc123" {
		t.Errorf("instance IDs = %v, want [i-abc123]", fake.lastStopIDs)
	}
}

func TestStopInstance_Error(t *testing.T) {
	fake := &fakeEC2Client{stopErr: errors.New("instance not found")}
	m := &ec2Service{client: fake}

	err := m.StopInstance(context.Background(), "i-missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	assertStringContains(t, err.Error(), "stopping instance i-missing")
	assertStringContains(t, err.Error(), "instance not found")
}

func TestStartInstance_Success(t *testing.T) {
	fake := &fakeEC2Client{}
	m := &ec2Service{client: fake}

	if err := m.StartInstance(context.Background(), "i-xyz789"); err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	if fake.startCalls != 1 {
		t.Errorf("StartInstances called %d times, want 1", fake.startCalls)
	}
	if len(fake.lastStartID) != 1 || fake.lastStartID[0] != "i-xyz789" {
		t.Errorf("instance IDs = %v, want [i-xyz789]", fake.lastStartID)
	}
}

func TestStartInstance_Error(t *testing.T) {
	fake := &fakeEC2Client{startErr: errors.New("throttled")}
	m := &ec2Service{client: fake}

	err := m.StartInstance(context.Background(), "i-xyz789")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	assertStringContains(t, err.Error(), "starting instance i-xyz789")
}

func TestEC2ServiceCapabilities_ReturnLoadConfigError(t *testing.T) {
	tests := []struct {
		name string
		run  func(*ec2Service) error
	}{
		{name: "stop", run: func(s *ec2Service) error {
			return s.StopInstance(context.Background(), "i-abc")
		}},
		{name: "start", run: func(s *ec2Service) error {
			return s.StartInstance(context.Background(), "i-abc")
		}},
		{name: "resolve AMI", run: func(s *ec2Service) error {
			_, err := s.ResolveUbuntuAMI(context.Background(), "us-east-1")
			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &ec2Service{
				awsCfg: awsConfig{region: "us-east-1"},
				loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
					return aws.Config{}, errors.New("no credentials")
				},
			}

			err := tc.run(s)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			assertStringContains(t, err.Error(), "loading AWS config for EC2 service")
		})
	}
}

func TestEC2ServiceEnsureClient_DefaultFactory(t *testing.T) {
	s := &ec2Service{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			return aws.Config{Region: "us-east-1"}, nil
		},
	}

	if err := s.ensureClient(context.Background()); err != nil {
		t.Fatalf("ensureClient: %v", err)
	}
	if s.client == nil {
		t.Fatal("ensureClient did not create the default EC2 client")
	}
}

func TestEC2ServiceEnsureClient_SharesClientAcrossCapabilities(t *testing.T) {
	fake := &fakeEC2ImagesClient{images: []types.Image{
		{ImageId: aws.String("ami-cached"), CreationDate: aws.String("2025-06-01T00:00:00Z")},
	}}
	loadCalls := 0
	newCalls := 0
	s := &ec2Service{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			loadCalls++
			return aws.Config{}, nil
		},
		newClient: func(_ aws.Config) ec2APIClient {
			newCalls++
			return fake
		},
	}

	if _, err := s.ResolveUbuntuAMI(context.Background(), "us-east-1"); err != nil {
		t.Fatalf("ResolveUbuntuAMI: %v", err)
	}
	if err := s.StartInstance(context.Background(), "i-1"); err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	if err := s.StopInstance(context.Background(), "i-1"); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}
	if loadCalls != 1 {
		t.Errorf("loadCfg called %d times, want 1", loadCalls)
	}
	if newCalls != 1 {
		t.Errorf("newClient called %d times, want 1", newCalls)
	}
}
