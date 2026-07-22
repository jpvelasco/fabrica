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
	if err := p.validateRunCommandArgs(instanceID, commands); err != nil {
		return fabricac.RemoteResult{}, err
	}

	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return fabricac.RemoteResult{}, err
	}
	client := p.ssmClient(cfg)

	cmdID, err := p.sendCommand(ctx, client, instanceID, commands)
	if err != nil {
		return fabricac.RemoteResult{}, err
	}

	return p.pollInvocation(ctx, client, cmdID, instanceID)
}

func (p *awsProvider) validateRunCommandArgs(instanceID string, commands []string) error {
	if instanceID == "" {
		return fmt.Errorf("instance ID is required for remote command")
	}
	if len(commands) == 0 {
		return fmt.Errorf("at least one command is required")
	}
	return nil
}

func (p *awsProvider) sendCommand(ctx context.Context, client ssmClient, instanceID string, commands []string) (string, error) {
	sendOut, err := client.SendCommand(ctx, &ssm.SendCommandInput{
		DocumentName: aws.String("AWS-RunShellScript"),
		InstanceIds:  []string{instanceID},
		Parameters: map[string][]string{
			"commands": commands,
		},
	})
	if err != nil {
		return "", fmt.Errorf("ssm SendCommand on %s: %w", instanceID, err)
	}
	if sendOut.Command == nil || sendOut.Command.CommandId == nil {
		return "", fmt.Errorf("ssm SendCommand on %s: empty command id", instanceID)
	}
	return aws.ToString(sendOut.Command.CommandId), nil
}

func (p *awsProvider) pollInvocation(ctx context.Context, client ssmClient, cmdID, instanceID string) (fabricac.RemoteResult, error) {
	deadline := time.Now().Add(ssmWaitTimeout)
	for {
		if err := p.checkDeadline(ctx, cmdID, instanceID, deadline); err != nil {
			return fabricac.RemoteResult{}, err
		}

		inv, err := client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
			CommandId:  aws.String(cmdID),
			InstanceId: aws.String(instanceID),
		})
		if err != nil {
			if !p.waitForNextPoll(ctx) {
				return fabricac.RemoteResult{}, ctx.Err()
			}
			continue
		}

		result, done, err := p.handleInvocationStatus(ctx, inv, cmdID, instanceID)
		if err != nil {
			return result, err
		}
		if done {
			return result, nil
		}
	}
}

func (p *awsProvider) checkDeadline(ctx context.Context, cmdID, instanceID string, deadline time.Time) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("waiting for ssm command %s: %w", cmdID, err)
	}
	if time.Now().After(deadline) {
		return fmt.Errorf("waiting for ssm command %s on %s: timed out after %s — check SSM agent and instance profile", cmdID, instanceID, ssmWaitTimeout)
	}
	return nil
}

func (p *awsProvider) waitForNextPoll(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(ssmPollInterval):
		return true
	}
}

func (p *awsProvider) handleInvocationStatus(ctx context.Context, inv *ssm.GetCommandInvocationOutput, cmdID, instanceID string) (fabricac.RemoteResult, bool, error) {
	status := inv.Status
	switch status {
	case ssmtypes.CommandInvocationStatusPending,
		ssmtypes.CommandInvocationStatusInProgress,
		ssmtypes.CommandInvocationStatusDelayed:
		if !p.waitForNextPoll(ctx) {
			return fabricac.RemoteResult{}, false, ctx.Err()
		}
		return fabricac.RemoteResult{}, false, nil
	case ssmtypes.CommandInvocationStatusSuccess:
		return p.resultFromInvocation(inv), true, nil
	default:
		return p.resultFromInvocation(inv), true,
			fmt.Errorf("ssm command %s on %s finished with status %s (exit %d): %s",
				cmdID, instanceID, status, int(inv.ResponseCode), aws.ToString(inv.StandardErrorContent))
	}
}

func (p *awsProvider) resultFromInvocation(inv *ssm.GetCommandInvocationOutput) fabricac.RemoteResult {
	return fabricac.RemoteResult{
		ExitCode: int(inv.ResponseCode),
		Stdout:   aws.ToString(inv.StandardOutputContent),
		Stderr:   aws.ToString(inv.StandardErrorContent),
	}
}

func (p *awsProvider) ssmClient(cfg aws.Config) ssmClient {
	if p.newSSMClient != nil {
		return p.newSSMClient(cfg)
	}
	return ssm.NewFromConfig(cfg)
}
