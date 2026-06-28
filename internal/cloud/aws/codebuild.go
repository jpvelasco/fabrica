package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	codebuildtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.CodeBuildRunner = (*awsProvider)(nil)

type codeBuildClient interface {
	StartBuild(context.Context, *codebuild.StartBuildInput, ...func(*codebuild.Options)) (*codebuild.StartBuildOutput, error)
	BatchGetBuilds(context.Context, *codebuild.BatchGetBuildsInput, ...func(*codebuild.Options)) (*codebuild.BatchGetBuildsOutput, error)
}

type cwLogsClient interface {
	GetLogEvents(context.Context, *cloudwatchlogs.GetLogEventsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error)
}

type codeBuildClientFactory func(aws.Config) codeBuildClient
type cwLogsClientFactory func(aws.Config) cwLogsClient

func (p *awsProvider) StartBuild(ctx context.Context, project string, env map[string]string) (string, error) {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	overrides := make([]codebuildtypes.EnvironmentVariable, 0, len(env))
	for k, v := range env {
		overrides = append(overrides, codebuildtypes.EnvironmentVariable{
			Name:  aws.String(k),
			Value: aws.String(v),
			Type:  codebuildtypes.EnvironmentVariableTypePlaintext,
		})
	}

	client := p.codeBuildClient(cfg)
	out, err := client.StartBuild(ctx, &codebuild.StartBuildInput{
		ProjectName:                  aws.String(project),
		EnvironmentVariablesOverride: overrides,
	})
	if err != nil {
		return "", fmt.Errorf("starting CodeBuild project %s: %w — check the project exists (fabrica ci setup) and you have codebuild:StartBuild", project, err)
	}
	if out.Build == nil || out.Build.Id == nil {
		return "", fmt.Errorf("CodeBuild did not return a build ID for project %s", project)
	}
	return aws.ToString(out.Build.Id), nil
}

func (p *awsProvider) BuildStatus(ctx context.Context, buildID string) (fabricac.BuildInfo, error) {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return fabricac.BuildInfo{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := p.codeBuildClient(cfg)
	out, err := client.BatchGetBuilds(ctx, &codebuild.BatchGetBuildsInput{Ids: []string{buildID}})
	if err != nil {
		return fabricac.BuildInfo{}, fmt.Errorf("getting CodeBuild build %s: %w", buildID, err)
	}
	if len(out.Builds) == 0 {
		return fabricac.BuildInfo{}, fmt.Errorf("CodeBuild build %s not found", buildID)
	}

	b := out.Builds[0]
	info := fabricac.BuildInfo{
		ID:     aws.ToString(b.Id),
		Status: string(b.BuildStatus),
		Phase:  aws.ToString(b.CurrentPhase),
	}
	if b.Logs != nil {
		info.LogGroup = aws.ToString(b.Logs.GroupName)
		info.LogStream = aws.ToString(b.Logs.StreamName)
	}
	return info, nil
}

func (p *awsProvider) BuildLog(ctx context.Context, buildID string) (string, error) {
	info, err := p.BuildStatus(ctx, buildID)
	if err != nil {
		return "", err
	}
	if info.LogGroup == "" || info.LogStream == "" {
		return "", fmt.Errorf("build %s has no logs yet (status: %s)", buildID, info.Status)
	}

	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := p.cwLogsClient(cfg)
	out, err := client.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
		LogGroupName:  aws.String(info.LogGroup),
		LogStreamName: aws.String(info.LogStream),
		StartFromHead: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("fetching logs for build %s: %w", buildID, err)
	}

	var sb strings.Builder
	for _, ev := range out.Events {
		sb.WriteString(aws.ToString(ev.Message))
	}
	return sb.String(), nil
}

func (p *awsProvider) codeBuildClient(cfg aws.Config) codeBuildClient {
	if p.newCodeBuildClient != nil {
		return p.newCodeBuildClient(cfg)
	}
	return codebuild.NewFromConfig(cfg)
}

func (p *awsProvider) cwLogsClient(cfg aws.Config) cwLogsClient {
	if p.newCWLogsClient != nil {
		return p.newCWLogsClient(cfg)
	}
	return cloudwatchlogs.NewFromConfig(cfg)
}
