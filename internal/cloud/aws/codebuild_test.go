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
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

type fakeCodeBuildClient struct {
	startInput *codebuild.StartBuildInput
	startID    string
	startErr   error
	builds     []codebuildtypes.Build
	batchErr   error
	batchedIDs []string

	createInput      *codebuild.CreateProjectInput
	createErr        error
	deletedProject   string
	existingProjects []codebuildtypes.Project
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

func (f *fakeCodeBuildClient) CreateProject(_ context.Context, in *codebuild.CreateProjectInput, _ ...func(*codebuild.Options)) (*codebuild.CreateProjectOutput, error) {
	f.createInput = in
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &codebuild.CreateProjectOutput{}, nil
}

func (f *fakeCodeBuildClient) DeleteProject(_ context.Context, in *codebuild.DeleteProjectInput, _ ...func(*codebuild.Options)) (*codebuild.DeleteProjectOutput, error) {
	f.deletedProject = awssdk.ToString(in.Name)
	return &codebuild.DeleteProjectOutput{}, nil
}

func (f *fakeCodeBuildClient) BatchGetProjects(_ context.Context, _ *codebuild.BatchGetProjectsInput, _ ...func(*codebuild.Options)) (*codebuild.BatchGetProjectsOutput, error) {
	return &codebuild.BatchGetProjectsOutput{Projects: f.existingProjects}, nil
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

func TestEnsureProjectCreatesWhenAbsent(t *testing.T) {
	cb := &fakeCodeBuildClient{} // no existing projects
	p := newCodeBuildTestProvider(cb, nil)

	created, err := p.EnsureProject(context.Background(), fabricac.CodeBuildProjectSpec{
		Name:           "fabrica-ci",
		ServiceRoleARN: "arn:aws:iam::123:role/fabrica-ci-codebuild",
		ComputeType:    "BUILD_GENERAL1_SMALL",
		Image:          "aws/codebuild/x:1",
		BuildTimeout:   60,
		Buildspec:      "version: 0.2",
		EnvDefaults:    map[string]string{"HORDE_URL": "http://10.0.0.5:5000"},
		Tags:           map[string]string{"ManagedBy": "fabrica"},
	})
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if !created {
		t.Error("created = false, want true")
	}
	if cb.createInput == nil || awssdk.ToString(cb.createInput.Name) != "fabrica-ci" {
		t.Errorf("CreateProject not called with project name")
	}
	if string(cb.createInput.Environment.ComputeType) != "BUILD_GENERAL1_SMALL" {
		t.Errorf("ComputeType = %q", cb.createInput.Environment.ComputeType)
	}
}

func TestEnsureProjectIdempotent(t *testing.T) {
	cb := &fakeCodeBuildClient{existingProjects: []codebuildtypes.Project{{Name: awssdk.String("fabrica-ci")}}}
	p := newCodeBuildTestProvider(cb, nil)

	created, err := p.EnsureProject(context.Background(), fabricac.CodeBuildProjectSpec{Name: "fabrica-ci"})
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if created {
		t.Error("created = true, want false for existing project")
	}
	if cb.createInput != nil {
		t.Error("CreateProject must not be called when project exists")
	}
}

func TestDeleteProject(t *testing.T) {
	cb := &fakeCodeBuildClient{}
	p := newCodeBuildTestProvider(cb, nil)
	if err := p.DeleteProject(context.Background(), "fabrica-ci"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if cb.deletedProject != "fabrica-ci" {
		t.Errorf("deleted = %q, want fabrica-ci", cb.deletedProject)
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
