package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// fakeEC2Client implements ec2APIClient for testing.
type fakeEC2Client struct {
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

func (f *fakeEC2Client) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{}, nil
}

func TestStopInstance_Success(t *testing.T) {
	fake := &fakeEC2Client{}
	m := &ec2Manager{client: fake}

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
	m := &ec2Manager{client: fake}

	err := m.StopInstance(context.Background(), "i-missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	assertStringContains(t, err.Error(), "stopping instance i-missing")
	assertStringContains(t, err.Error(), "instance not found")
}

func TestStartInstance_Success(t *testing.T) {
	fake := &fakeEC2Client{}
	m := &ec2Manager{client: fake}

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
	m := &ec2Manager{client: fake}

	err := m.StartInstance(context.Background(), "i-xyz789")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	assertStringContains(t, err.Error(), "starting instance i-xyz789")
}

func TestEC2ManagerEnsureClient_LoadConfigError(t *testing.T) {
	m := &ec2Manager{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			return aws.Config{}, errors.New("no credentials")
		},
	}

	err := m.StopInstance(context.Background(), "i-abc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	assertStringContains(t, err.Error(), "loading AWS config for EC2 manager")
}

func TestEC2ManagerEnsureClient_UsesSeamAndCaches(t *testing.T) {
	fake := &fakeEC2Client{}
	loadCalls := 0
	newCalls := 0
	m := &ec2Manager{
		awsCfg: awsConfig{region: "us-east-1"},
		loadCfg: func(_ context.Context, _, _ string) (aws.Config, error) {
			loadCalls++
			return aws.Config{}, nil
		},
		newClient: func(aws.Config) ec2APIClient {
			newCalls++
			return fake
		},
	}

	// Two calls should construct the client exactly once (cached thereafter).
	if err := m.StartInstance(context.Background(), "i-1"); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := m.StopInstance(context.Background(), "i-1"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if loadCalls != 1 {
		t.Errorf("loadCfg called %d times, want 1 (client should be cached)", loadCalls)
	}
	if newCalls != 1 {
		t.Errorf("newClient called %d times, want 1", newCalls)
	}
}
