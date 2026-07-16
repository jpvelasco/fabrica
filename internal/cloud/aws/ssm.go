package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

const ssmPollInterval = 2 * time.Second

// ssmWaitTimeout is the max time RunCommand waits for an invocation.
// Overridable in tests.
var ssmWaitTimeout = 15 * time.Minute

var _ fabricac.RemoteRunner = (*awsProvider)(nil)

type ssmClient interface {
	SendCommand(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}

type ssmClientFactory func(aws.Config) ssmClient

// RunCommand executes shell commands on instanceID via SSM Run Command
// (AWS-RunShellScript) and waits for the invocation to finish.
func (p *awsProvider) RunCommand(ctx context.Context, instanceID string, commands []string) (fabricac.RemoteResult, error) {
	if instanceID == "" {
		return fabricac.RemoteResult{}, fmt.Errorf("instance ID is required for remote command")
	}
	if len(commands) == 0 {
		return fabricac.RemoteResult{}, fmt.Errorf("at least one command is required")
	}

	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return fabricac.RemoteResult{}, err
	}
	client := p.ssmClient(cfg)

	sendOut, err := client.SendCommand(ctx, &ssm.SendCommandInput{
		DocumentName: aws.String("AWS-RunShellScript"),
		InstanceIds:  []string{instanceID},
		Parameters: map[string][]string{
			"commands": commands,
		},
	})
	if err != nil {
		return fabricac.RemoteResult{}, fmt.Errorf("ssm SendCommand on %s: %w", instanceID, err)
	}
	if sendOut.Command == nil || sendOut.Command.CommandId == nil {
		return fabricac.RemoteResult{}, fmt.Errorf("ssm SendCommand on %s: empty command id", instanceID)
	}
	cmdID := aws.ToString(sendOut.Command.CommandId)

	deadline := time.Now().Add(ssmWaitTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return fabricac.RemoteResult{}, fmt.Errorf("waiting for ssm command %s: %w", cmdID, err)
		}
		if time.Now().After(deadline) {
			return fabricac.RemoteResult{}, fmt.Errorf("waiting for ssm command %s on %s: timed out after %s — check SSM agent and instance profile", cmdID, instanceID, ssmWaitTimeout)
		}

		inv, err := client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
			CommandId:  aws.String(cmdID),
			InstanceId: aws.String(instanceID),
		})
		if err != nil {
			// Invocation may not be ready immediately after SendCommand.
			select {
			case <-ctx.Done():
				return fabricac.RemoteResult{}, ctx.Err()
			case <-time.After(ssmPollInterval):
				continue
			}
		}

		status := inv.Status
		switch status {
		case ssmtypes.CommandInvocationStatusPending,
			ssmtypes.CommandInvocationStatusInProgress,
			ssmtypes.CommandInvocationStatusDelayed:
			select {
			case <-ctx.Done():
				return fabricac.RemoteResult{}, ctx.Err()
			case <-time.After(ssmPollInterval):
				continue
			}
		case ssmtypes.CommandInvocationStatusSuccess:
			return fabricac.RemoteResult{
				ExitCode: int(inv.ResponseCode),
				Stdout:   aws.ToString(inv.StandardOutputContent),
				Stderr:   aws.ToString(inv.StandardErrorContent),
			}, nil
		default:
			// Failed / Cancelled / TimedOut / Cancelling
			exit := int(inv.ResponseCode)
			stdout := aws.ToString(inv.StandardOutputContent)
			stderr := aws.ToString(inv.StandardErrorContent)
			return fabricac.RemoteResult{
					ExitCode: exit,
					Stdout:   stdout,
					Stderr:   stderr,
				}, fmt.Errorf("ssm command %s on %s finished with status %s (exit %d): %s",
					cmdID, instanceID, status, exit, stderr)
		}
	}
}

func (p *awsProvider) ssmClient(cfg aws.Config) ssmClient {
	if p.newSSMClient != nil {
		return p.newSSMClient(cfg)
	}
	return ssm.NewFromConfig(cfg)
}
