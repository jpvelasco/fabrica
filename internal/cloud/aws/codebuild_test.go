package aws

import (
	"context"
	"fmt"
	"strings"
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
	// Single-page convenience (legacy): returned on every call.
	events []cwltypes.OutputLogEvent
	err    error

	// Scripted pagination: pages[i] is returned on call i, with tokens[i] as
	// NextForwardToken. When calls exceed len(pages), the last page repeats.
	pages  [][]cwltypes.OutputLogEvent
	tokens []string
	calls  int
}

func (f *fakeCWLogsClient) GetLogEvents(_ context.Context, in *cloudwatchlogs.GetLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.pages == nil {
		return &cloudwatchlogs.GetLogEventsOutput{Events: f.events}, nil
	}
	i := f.calls - 1
	if i >= len(f.pages) {
		// Exhausted stream: CloudWatch returns no events plus the same
		// forward token that was sent.
		return &cloudwatchlogs.GetLogEventsOutput{
			Events:           []cwltypes.OutputLogEvent{},
			NextForwardToken: awssdk.String(f.tokens[len(f.tokens)-1]),
		}, nil
	}
	var token string
	if i < len(f.tokens) {
		token = f.tokens[i]
	}
	return &cloudwatchlogs.GetLogEventsOutput{
		Events:           f.pages[i],
		NextForwardToken: awssdk.String(token),
	}, nil
}

func newCodeBuildTestProvider(cb *fakeCodeBuildClient, logs *fakeCWLogsClient) *awsProvider {
	return &awsProvider{
		awsCfg:  awsConfig{region: "us-east-1", profile: "unit-test"},
		clients: resourceClients{version: "v9.9.9"},
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
	// FabricaVersion is injected at the SDK boundary (mirrors Cloud Control).
	tagMap := map[string]string{}
	for _, tg := range cb.createInput.Tags {
		tagMap[awssdk.ToString(tg.Key)] = awssdk.ToString(tg.Value)
	}
	if tagMap["FabricaVersion"] != "v9.9.9" {
		t.Errorf("FabricaVersion tag = %q, want v9.9.9 (tags: %v)", tagMap["FabricaVersion"], tagMap)
	}
	if tagMap["ManagedBy"] != "fabrica" {
		t.Errorf("ManagedBy tag = %q, want fabrica", tagMap["ManagedBy"])
	}
	// No VPC config → the SDK VpcConfig must be nil.
	if cb.createInput.VpcConfig != nil {
		t.Errorf("VpcConfig = %+v, want nil when spec has no VPC", cb.createInput.VpcConfig)
	}
}

func TestEnsureProjectSetsVpcConfig(t *testing.T) {
	cb := &fakeCodeBuildClient{}
	p := newCodeBuildTestProvider(cb, nil)

	created, err := p.EnsureProject(context.Background(), fabricac.CodeBuildProjectSpec{
		Name:           "fabrica-ci",
		ServiceRoleARN: "arn:aws:iam::123:role/fabrica-ci-codebuild",
		ComputeType:    "BUILD_GENERAL1_SMALL",
		Image:          "aws/codebuild/x:1",
		BuildTimeout:   60,
		Buildspec:      "version: 0.2",
		Tags:           map[string]string{"ManagedBy": "fabrica"},
		VpcConfig: &fabricac.CodeBuildVpcConfig{
			VpcID:            "vpc-abc",
			SubnetID:         "subnet-xyz",
			SecurityGroupIDs: []string{"sg-123"},
		},
	})
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if !created {
		t.Error("created = false, want true")
	}
	if cb.createInput == nil || cb.createInput.VpcConfig == nil {
		t.Fatal("VpcConfig = nil, want set")
	}
	if awssdk.ToString(cb.createInput.VpcConfig.VpcId) != "vpc-abc" {
		t.Errorf("VpcId = %q, want vpc-abc", awssdk.ToString(cb.createInput.VpcConfig.VpcId))
	}
	if len(cb.createInput.VpcConfig.Subnets) != 1 || cb.createInput.VpcConfig.Subnets[0] != "subnet-xyz" {
		t.Errorf("Subnets = %v, want [subnet-xyz]", cb.createInput.VpcConfig.Subnets)
	}
	if len(cb.createInput.VpcConfig.SecurityGroupIds) != 1 || cb.createInput.VpcConfig.SecurityGroupIds[0] != "sg-123" {
		t.Errorf("SecurityGroupIds = %v, want [sg-123]", cb.createInput.VpcConfig.SecurityGroupIds)
	}
}

func TestVpcConfigPartialNil(t *testing.T) {
	// A spec with a VPC ID but no subnet must not produce an SDK VpcConfig —
	// an invalid half-configured VPC would make the CreateProject call fail.
	if vc := vpcConfig(&fabricac.CodeBuildVpcConfig{VpcID: "vpc-abc"}); vc != nil {
		t.Errorf("vpcConfig(vpc only) = %+v, want nil", vc)
	}
	if vc := vpcConfig(nil); vc != nil {
		t.Errorf("vpcConfig(nil) = %+v, want nil", vc)
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

// TestBuildLogFollowsPagination verifies BuildLog follows NextForwardToken
// across pages and stops when the stream is exhausted (same token returned).
func TestBuildLogFollowsPagination(t *testing.T) {
	cb := &fakeCodeBuildClient{builds: []codebuildtypes.Build{{
		Id:          awssdk.String("build-123"),
		BuildStatus: codebuildtypes.StatusTypeSucceeded,
		Logs: &codebuildtypes.LogsLocation{
			GroupName:  awssdk.String("/aws/codebuild/fabrica-ci"),
			StreamName: awssdk.String("abc"),
		},
	}}}
	logs := &fakeCWLogsClient{
		pages: [][]cwltypes.OutputLogEvent{
			{{Message: awssdk.String("page1\n")}},
			{{Message: awssdk.String("page2\n")}},
			{{Message: awssdk.String("page3\n")}},
		},
		tokens: []string{"t1", "t2", "t3"},
	}
	p := newCodeBuildTestProvider(cb, logs)

	out, err := p.BuildLog(context.Background(), "build-123")
	if err != nil {
		t.Fatalf("BuildLog: %v", err)
	}
	if out != "page1\npage2\npage3\n" {
		t.Errorf("log = %q, want all three pages in order", out)
	}
	// One extra call confirms exhaustion via the repeated token, then stops.
	if logs.calls != 4 {
		t.Errorf("GetLogEvents calls = %d, want 4 (3 pages + 1 exhausted check)", logs.calls)
	}
}

// TestBuildLogStopsOnEmptyPage verifies an empty page terminates pagination
// even if a token is present.
func TestBuildLogStopsOnEmptyPage(t *testing.T) {
	cb := &fakeCodeBuildClient{builds: []codebuildtypes.Build{{
		Id:          awssdk.String("build-123"),
		BuildStatus: codebuildtypes.StatusTypeSucceeded,
		Logs: &codebuildtypes.LogsLocation{
			GroupName:  awssdk.String("/aws/codebuild/fabrica-ci"),
			StreamName: awssdk.String("abc"),
		},
	}}}
	logs := &fakeCWLogsClient{
		pages: [][]cwltypes.OutputLogEvent{
			{{Message: awssdk.String("only\n")}},
			{}, // empty page
		},
		tokens: []string{"t1", "t2"},
	}
	p := newCodeBuildTestProvider(cb, logs)

	out, err := p.BuildLog(context.Background(), "build-123")
	if err != nil {
		t.Fatalf("BuildLog: %v", err)
	}
	if out != "only\n" {
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

func TestStartBuildNilBuild(t *testing.T) {
	p := &awsProvider{
		awsCfg:  awsConfig{region: "us-east-1", profile: "unit-test"},
		clients: resourceClients{version: "v9.9.9"},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{Region: region}, nil
		},
		newCodeBuildClient: func(awssdk.Config) codeBuildClient {
			return &codeBuildNilBuildClient{}
		},
	}
	_, err := p.StartBuild(context.Background(), "fabrica-ci", nil)
	if err == nil {
		t.Fatal("expected error when Build is nil")
	}
	if got := err.Error(); !strings.Contains(got, "CodeBuild did not return a build ID") {
		t.Fatalf("error = %q, want substring %q", got, "CodeBuild did not return a build ID")
	}
}

// codeBuildNilBuildClient always returns a StartBuildOutput with nil Build.
type codeBuildNilBuildClient struct{ fakeCodeBuildClient }

func (c *codeBuildNilBuildClient) StartBuild(_ context.Context, _ *codebuild.StartBuildInput, _ ...func(*codebuild.Options)) (*codebuild.StartBuildOutput, error) {
	return &codebuild.StartBuildOutput{}, nil
}

func TestStartBuildNilBuildID(t *testing.T) {
	p := &awsProvider{
		awsCfg:  awsConfig{region: "us-east-1", profile: "unit-test"},
		clients: resourceClients{version: "v9.9.9"},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{Region: region}, nil
		},
		newCodeBuildClient: func(awssdk.Config) codeBuildClient {
			return &codeBuildNilBuildIDClient{}
		},
	}
	_, err := p.StartBuild(context.Background(), "fabrica-ci", nil)
	if err == nil {
		t.Fatal("expected error when Build.Id is nil")
	}
	if got := err.Error(); !strings.Contains(got, "CodeBuild did not return a build ID") {
		t.Fatalf("error = %q, want substring %q", got, "CodeBuild did not return a build ID")
	}
}

// codeBuildNilBuildIDClient returns a Build with nil Id.
type codeBuildNilBuildIDClient struct{ fakeCodeBuildClient }

func (c *codeBuildNilBuildIDClient) StartBuild(_ context.Context, _ *codebuild.StartBuildInput, _ ...func(*codebuild.Options)) (*codebuild.StartBuildOutput, error) {
	return &codebuild.StartBuildOutput{Build: &codebuildtypes.Build{}}, nil
}

func TestBuildStatusSDKError(t *testing.T) {
	cb := &fakeCodeBuildClient{batchErr: fmt.Errorf("service unavailable")}
	p := newCodeBuildTestProvider(cb, nil)
	_, err := p.BuildStatus(context.Background(), "build-123")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "getting CodeBuild build") {
		t.Fatalf("error = %q, want substring %q", got, "getting CodeBuild build")
	}
}

func TestBuildLogSDKError(t *testing.T) {
	cb := &fakeCodeBuildClient{builds: []codebuildtypes.Build{{
		Id:          awssdk.String("build-123"),
		BuildStatus: codebuildtypes.StatusTypeSucceeded,
		Logs: &codebuildtypes.LogsLocation{
			GroupName:  awssdk.String("/aws/codebuild/fabrica-ci"),
			StreamName: awssdk.String("abc"),
		},
	}}}
	logs := &fakeCWLogsClient{err: fmt.Errorf("resource not found")}
	p := newCodeBuildTestProvider(cb, logs)
	_, err := p.BuildLog(context.Background(), "build-123")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !strings.Contains(got, "fetching logs for build") {
		t.Fatalf("error = %q, want substring %q", got, "fetching logs for build")
	}
}

func TestProjectExistsTrue(t *testing.T) {
	cb := &fakeCodeBuildClient{existingProjects: []codebuildtypes.Project{{Name: awssdk.String("fabrica-ci")}}}
	p := newCodeBuildTestProvider(cb, nil)
	exists, err := p.ProjectExists(context.Background(), "fabrica-ci")
	if err != nil {
		t.Fatalf("ProjectExists: %v", err)
	}
	if !exists {
		t.Error("exists = false, want true")
	}
}

func TestProjectExistsFalse(t *testing.T) {
	cb := &fakeCodeBuildClient{} // no existing projects
	p := newCodeBuildTestProvider(cb, nil)
	exists, err := p.ProjectExists(context.Background(), "fabrica-ci")
	if err != nil {
		t.Fatalf("ProjectExists: %v", err)
	}
	if exists {
		t.Error("exists = true, want false")
	}
}
