package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/jpvelasco/fabrica/internal/config"
)

type mockSSM struct {
	sendFn func(context.Context, *ssm.SendCommandInput) (*ssm.SendCommandOutput, error)
	getFn  func(context.Context, *ssm.GetCommandInvocationInput) (*ssm.GetCommandInvocationOutput, error)
	gets   int
}

func (m *mockSSM) SendCommand(ctx context.Context, in *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	return m.sendFn(ctx, in)
}

func (m *mockSSM) GetCommandInvocation(ctx context.Context, in *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	m.gets++
	return m.getFn(ctx, in)
}

func testProviderWithSSM(m *mockSSM) *awsProvider {
	p := &awsProvider{
		cfg: &config.Config{},
		awsCfg: awsConfig{
			region: "us-east-1",
		},
		newSSMClient: func(aws.Config) ssmClient { return m },
		loadConfig: func(ctx context.Context, region, profile string) (aws.Config, error) {
			return aws.Config{Region: region}, nil
		},
	}
	return p
}

func TestRunCommand_Success(t *testing.T) {
	m := &mockSSM{
		sendFn: func(_ context.Context, in *ssm.SendCommandInput) (*ssm.SendCommandOutput, error) {
			if len(in.InstanceIds) != 1 || in.InstanceIds[0] != "i-abc" {
				t.Fatalf("instance ids: %v", in.InstanceIds)
			}
			return &ssm.SendCommandOutput{
				Command: &ssmtypes.Command{CommandId: aws.String("cmd-1")},
			}, nil
		},
		getFn: func(_ context.Context, _ *ssm.GetCommandInvocationInput) (*ssm.GetCommandInvocationOutput, error) {
			return &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				ResponseCode:          0,
				StandardOutputContent: aws.String("BACKUP_OK"),
			}, nil
		},
	}
	p := testProviderWithSSM(m)
	res, err := p.RunCommand(context.Background(), "i-abc", []string{"echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "BACKUP_OK" || res.ExitCode != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunCommand_FailedStatus(t *testing.T) {
	m := &mockSSM{
		sendFn: func(_ context.Context, _ *ssm.SendCommandInput) (*ssm.SendCommandOutput, error) {
			return &ssm.SendCommandOutput{
				Command: &ssmtypes.Command{CommandId: aws.String("cmd-1")},
			}, nil
		},
		getFn: func(_ context.Context, _ *ssm.GetCommandInvocationInput) (*ssm.GetCommandInvocationOutput, error) {
			return &ssm.GetCommandInvocationOutput{
				Status:               ssmtypes.CommandInvocationStatusFailed,
				ResponseCode:         1,
				StandardErrorContent: aws.String("boom"),
			}, nil
		},
	}
	p := testProviderWithSSM(m)
	_, err := p.RunCommand(context.Background(), "i-abc", []string{"false"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, err) && err.Error() == "" {
		t.Fatal("expected non-empty error")
	}
}

func TestRunCommand_Validation(t *testing.T) {
	p := testProviderWithSSM(&mockSSM{})
	if _, err := p.RunCommand(context.Background(), "", []string{"x"}); err == nil {
		t.Fatal("expected empty instance error")
	}
	if _, err := p.RunCommand(context.Background(), "i-1", nil); err == nil {
		t.Fatal("expected empty commands error")
	}
}

func TestRunCommand_SendError(t *testing.T) {
	m := &mockSSM{
		sendFn: func(_ context.Context, _ *ssm.SendCommandInput) (*ssm.SendCommandOutput, error) {
			return nil, errors.New("denied")
		},
	}
	p := testProviderWithSSM(m)
	if _, err := p.RunCommand(context.Background(), "i-1", []string{"x"}); err == nil {
		t.Fatal("expected send error")
	}
}

func TestRunCommand_PendingThenSuccess(t *testing.T) {
	m := &mockSSM{
		sendFn: func(_ context.Context, _ *ssm.SendCommandInput) (*ssm.SendCommandOutput, error) {
			return &ssm.SendCommandOutput{
				Command: &ssmtypes.Command{CommandId: aws.String("cmd-2")},
			}, nil
		},
		getFn: func(_ context.Context, _ *ssm.GetCommandInvocationInput) (*ssm.GetCommandInvocationOutput, error) {
			// first call pending via gets count
			return &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				ResponseCode:          0,
				StandardOutputContent: aws.String("ok"),
				StandardErrorContent:  aws.String(""),
			}, nil
		},
	}
	// Force one pending iteration by alternating status using gets.
	n := 0
	m.getFn = func(_ context.Context, _ *ssm.GetCommandInvocationInput) (*ssm.GetCommandInvocationOutput, error) {
		n++
		if n == 1 {
			return &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusInProgress}, nil
		}
		return &ssm.GetCommandInvocationOutput{
			Status:                ssmtypes.CommandInvocationStatusSuccess,
			ResponseCode:          0,
			StandardOutputContent: aws.String("ok"),
		}, nil
	}
	p := testProviderWithSSM(m)
	res, err := p.RunCommand(context.Background(), "i-abc", []string{"echo"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "ok" {
		t.Fatalf("stdout = %q", res.Stdout)
	}
}

func TestRunCommand_EmptyCommandID(t *testing.T) {
	m := &mockSSM{
		sendFn: func(_ context.Context, _ *ssm.SendCommandInput) (*ssm.SendCommandOutput, error) {
			return &ssm.SendCommandOutput{Command: &ssmtypes.Command{}}, nil
		},
	}
	p := testProviderWithSSM(m)
	if _, err := p.RunCommand(context.Background(), "i-1", []string{"x"}); err == nil {
		t.Fatal("expected empty command id error")
	}
}

func TestRunCommand_NilCommand(t *testing.T) {
	m := &mockSSM{
		sendFn: func(_ context.Context, _ *ssm.SendCommandInput) (*ssm.SendCommandOutput, error) {
			return &ssm.SendCommandOutput{}, nil
		},
	}
	p := testProviderWithSSM(m)
	if _, err := p.RunCommand(context.Background(), "i-1", []string{"x"}); err == nil {
		t.Fatal("expected empty command id error")
	}
}

func TestSSMClientDefaultConstructor(t *testing.T) {
	p := &awsProvider{}
	c := p.ssmClient(aws.Config{Region: "us-east-1"})
	if c == nil {
		t.Fatal("expected non-nil ssm client")
	}
}

func TestRunCommand_GetErrorThenSuccess(t *testing.T) {
	n := 0
	m := &mockSSM{
		sendFn: func(_ context.Context, _ *ssm.SendCommandInput) (*ssm.SendCommandOutput, error) {
			return &ssm.SendCommandOutput{
				Command: &ssmtypes.Command{CommandId: aws.String("cmd-3")},
			}, nil
		},
		getFn: func(_ context.Context, _ *ssm.GetCommandInvocationInput) (*ssm.GetCommandInvocationOutput, error) {
			n++
			if n == 1 {
				return nil, errors.New("invocation not ready")
			}
			return &ssm.GetCommandInvocationOutput{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				ResponseCode:          0,
				StandardOutputContent: aws.String("done"),
			}, nil
		},
	}
	p := testProviderWithSSM(m)
	res, err := p.RunCommand(context.Background(), "i-abc", []string{"echo"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "done" {
		t.Fatalf("stdout=%q", res.Stdout)
	}
}

func TestRunCommand_DelayedThenSuccess(t *testing.T) {
	n := 0
	m := &mockSSM{
		sendFn: func(_ context.Context, _ *ssm.SendCommandInput) (*ssm.SendCommandOutput, error) {
			return &ssm.SendCommandOutput{
				Command: &ssmtypes.Command{CommandId: aws.String("cmd-4")},
			}, nil
		},
		getFn: func(_ context.Context, _ *ssm.GetCommandInvocationInput) (*ssm.GetCommandInvocationOutput, error) {
			n++
			if n == 1 {
				return &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusDelayed}, nil
			}
			return &ssm.GetCommandInvocationOutput{
				Status:       ssmtypes.CommandInvocationStatusSuccess,
				ResponseCode: 0,
			}, nil
		},
	}
	p := testProviderWithSSM(m)
	if _, err := p.RunCommand(context.Background(), "i-abc", []string{"echo"}); err != nil {
		t.Fatal(err)
	}
}
