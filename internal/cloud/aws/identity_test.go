package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// fakeSTSClient implements stsAPIClient for testing.
type fakeSTSClient struct {
	out *sts.GetCallerIdentityOutput
	err error
}

func (f *fakeSTSClient) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

// withIdentitySeams swaps the package-level identity seams for the duration of
// the test and restores them afterwards.
func withIdentitySeams(t *testing.T, loadCfg func(context.Context, string, string) (aws.Config, error), client stsAPIClient) {
	t.Helper()
	origLoad, origNew := identityLoadCfg, identityNewClient
	identityLoadCfg = loadCfg
	identityNewClient = func(aws.Config) stsAPIClient { return client }
	t.Cleanup(func() {
		identityLoadCfg = origLoad
		identityNewClient = origNew
	})
}

func okConfig(region string) func(context.Context, string, string) (aws.Config, error) {
	return func(_ context.Context, _, _ string) (aws.Config, error) {
		return aws.Config{Region: region}, nil
	}
}

func TestCallerIdentity_Success(t *testing.T) {
	withIdentitySeams(t, okConfig("us-west-2"), &fakeSTSClient{
		out: &sts.GetCallerIdentityOutput{
			Account: aws.String("123456789012"),
			Arn:     aws.String("arn:aws:iam::123456789012:user/jp"),
		},
	})

	account, arn, region, err := callerIdentity(context.Background(), awsConfig{region: "us-west-2"})
	if err != nil {
		t.Fatalf("callerIdentity: %v", err)
	}
	if account != "123456789012" {
		t.Errorf("account = %q, want 123456789012", account)
	}
	if arn != "arn:aws:iam::123456789012:user/jp" {
		t.Errorf("arn = %q", arn)
	}
	if region != "us-west-2" {
		t.Errorf("region = %q, want us-west-2", region)
	}
}

func TestCallerIdentity_ConfigLoadError(t *testing.T) {
	withIdentitySeams(t,
		func(_ context.Context, _, _ string) (aws.Config, error) {
			return aws.Config{}, errors.New("no shared credentials")
		},
		&fakeSTSClient{},
	)

	_, _, _, err := callerIdentity(context.Background(), awsConfig{region: "us-east-1"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	assertStringContains(t, err.Error(), "loading AWS config")
}

func TestCallerIdentity_STSError(t *testing.T) {
	withIdentitySeams(t, okConfig("us-east-1"), &fakeSTSClient{
		err: errors.New("AccessDenied: not authorized to perform sts:GetCallerIdentity"),
	})

	_, _, _, err := callerIdentity(context.Background(), awsConfig{region: "us-east-1"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	assertStringContains(t, err.Error(), "calling sts:GetCallerIdentity")
	assertStringContains(t, err.Error(), "AccessDenied")
}

func TestCallerIdentity_NilAccountDoesNotPanic(t *testing.T) {
	// A nil Account in the STS response must not panic (defensive against
	// unusual identities / mocked responses).
	withIdentitySeams(t, okConfig("eu-west-1"), &fakeSTSClient{
		out: &sts.GetCallerIdentityOutput{
			Arn: aws.String("arn:aws:sts::000000000000:assumed-role/x/y"),
		},
	})

	account, _, region, err := callerIdentity(context.Background(), awsConfig{region: "eu-west-1"})
	if err != nil {
		t.Fatalf("callerIdentity: %v", err)
	}
	if account != "" {
		t.Errorf("account = %q, want empty for nil Account", account)
	}
	if region != "eu-west-1" {
		t.Errorf("region = %q", region)
	}
}
