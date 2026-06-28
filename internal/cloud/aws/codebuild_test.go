package aws

import (
	"context"
	"fmt"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	codebuildtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
)

type fakeCodeBuildClient struct {
	startInput *codebuild.StartBuildInput
	startID    string
	startErr   error
	builds     []codebuildtypes.Build
	batchErr   error
	batchedIDs []string
}

func (f *fakeCodeBuildClient) StartBuild(_ context.Context, in *codebuild.StartBuildInput, _ ...func(*codebuild.Options)) (*codebuild.StartBuildOutput, error) {
	f.startInput = in
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &codebuild.StartBuildOutput{Build: &codebuildtypes.Build{Id: awssdk.String(f.startID)}}, nil
}

func (f *fakeCodeBuildClient) BatchGetBuilds(_ context.Context, in *codebuild.BatchGetBuildsInput, _ ...func(*codebuild.Options)) (*codebuild.BatchGetBuildsOutput, error) {
	f.batchedIDs = in.Ids
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	return &codebuild.BatchGetBuildsOutput{Builds: f.builds}, nil
}

type fakeCWLogsClient struct {
	events []cwltypes.OutputLogEvent
	err    error
}

func (f *fakeCWLogsClient) GetLogEvents(_ context.Context, _ *cloudwatchlogs.GetLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &cloudwatchlogs.GetLogEventsOutput{Events: f.events}, nil
}

func newCodeBuildTestProvider(cb *fakeCodeBuildClient, logs *fakeCWLogsClient) *awsProvider {
	return &awsProvider{
		awsCfg: awsConfig{region: "us-east-1", profile: "unit-test"},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{Region: region}, nil
		},
		newCodeBuildClient: func(awssdk.Config) codeBuildClient { return cb },
		newCWLogsClient:    func(awssdk.Config) cwLogsClient { return logs },
	}
}

func TestStartBuildPassesEnvOverrides(t *testing.T) {
	cb := &fakeCodeBuildClient{startID: "build-123"}
	p := newCodeBuildTestProvider(cb, nil)

	id, err := p.StartBuild(context.Background(), "fabrica-ci", map[string]string{"TARGET": "Compile", "HORDE_URL": "http://10.0.0.5:5000"})
	if err != nil {
		t.Fatalf("StartBuild: %v", err)
	}
	if id != "build-123" {
		t.Errorf("id = %q, want build-123", id)
	}
	if awssdk.ToString(cb.startInput.ProjectName) != "fabrica-ci" {
		t.Errorf("project = %q", awssdk.ToString(cb.startInput.ProjectName))
	}
	if len(cb.startInput.EnvironmentVariablesOverride) != 2 {
		t.Errorf("env overrides = %d, want 2", len(cb.startInput.EnvironmentVariablesOverride))
	}
}

func TestStartBuildError(t *testing.T) {
	cb := &fakeCodeBuildClient{startErr: fmt.Errorf("AccessDenied")}
	p := newCodeBuildTestProvider(cb, nil)
	if _, err := p.StartBuild(context.Background(), "fabrica-ci", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildStatusMapsFields(t *testing.T) {
	cb := &fakeCodeBuildClient{builds: []codebuildtypes.Build{{
		Id:           awssdk.String("build-123"),
		BuildStatus:  codebuildtypes.StatusTypeInProgress,
		CurrentPhase: awssdk.String("BUILD"),
		Logs: &codebuildtypes.LogsLocation{
			GroupName:  awssdk.String("/aws/codebuild/fabrica-ci"),
			StreamName: awssdk.String("abc"),
		},
	}}}
	p := newCodeBuildTestProvider(cb, nil)

	info, err := p.BuildStatus(context.Background(), "build-123")
	if err != nil {
		t.Fatalf("BuildStatus: %v", err)
	}
	if info.Status != "IN_PROGRESS" || info.Phase != "BUILD" {
		t.Errorf("status/phase = %q/%q", info.Status, info.Phase)
	}
	if info.LogGroup != "/aws/codebuild/fabrica-ci" || info.LogStream != "abc" {
		t.Errorf("logs = %q/%q", info.LogGroup, info.LogStream)
	}
}

func TestBuildStatusNotFound(t *testing.T) {
	cb := &fakeCodeBuildClient{builds: nil}
	p := newCodeBuildTestProvider(cb, nil)
	if _, err := p.BuildStatus(context.Background(), "build-x"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestBuildLogConcatenatesEvents(t *testing.T) {
	cb := &fakeCodeBuildClient{builds: []codebuildtypes.Build{{
		Id:          awssdk.String("build-123"),
		BuildStatus: codebuildtypes.StatusTypeSucceeded,
		Logs: &codebuildtypes.LogsLocation{
			GroupName:  awssdk.String("/aws/codebuild/fabrica-ci"),
			StreamName: awssdk.String("abc"),
		},
	}}}
	logs := &fakeCWLogsClient{events: []cwltypes.OutputLogEvent{
		{Message: awssdk.String("line1\n")},
		{Message: awssdk.String("line2\n")},
	}}
	p := newCodeBuildTestProvider(cb, logs)

	out, err := p.BuildLog(context.Background(), "build-123")
	if err != nil {
		t.Fatalf("BuildLog: %v", err)
	}
	if out != "line1\nline2\n" {
		t.Errorf("log = %q", out)
	}
}

func TestBuildLogNoLogsYet(t *testing.T) {
	cb := &fakeCodeBuildClient{builds: []codebuildtypes.Build{{
		Id:          awssdk.String("build-123"),
		BuildStatus: codebuildtypes.StatusTypeInProgress,
	}}}
	p := newCodeBuildTestProvider(cb, nil)
	if _, err := p.BuildLog(context.Background(), "build-123"); err == nil {
		t.Fatal("expected error when no logs available")
	}
}
