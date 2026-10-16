package aws

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

type mockParameter struct {
	putFn func(context.Context, *ssm.PutParameterInput) (*ssm.PutParameterOutput, error)
	delFn func(context.Context, *ssm.DeleteParameterInput) (*ssm.DeleteParameterOutput, error)
	tagFn func(context.Context, *ssm.AddTagsToResourceInput) (*ssm.AddTagsToResourceOutput, error)
	puts  int
	dels  int
	tags  int
}

func (m *mockParameter) PutParameter(ctx context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	m.puts++
	if m.putFn != nil {
		return m.putFn(ctx, in)
	}
	return &ssm.PutParameterOutput{}, nil
}

func (m *mockParameter) DeleteParameter(ctx context.Context, in *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	m.dels++
	if m.delFn != nil {
		return m.delFn(ctx, in)
	}
	return &ssm.DeleteParameterOutput{}, nil
}

func (m *mockParameter) AddTagsToResource(ctx context.Context, in *ssm.AddTagsToResourceInput, _ ...func(*ssm.Options)) (*ssm.AddTagsToResourceOutput, error) {
	m.tags++
	if m.tagFn != nil {
		return m.tagFn(ctx, in)
	}
	return &ssm.AddTagsToResourceOutput{}, nil
}

func testProviderWithParameter(m *mockParameter) *awsProvider {
	return &awsProvider{
		cfg:    &config.Config{},
		awsCfg: awsConfig{region: "us-east-1"},
		newParameterClient: func(aws.Config) parameterClient {
			return m
		},
		loadConfig: func(_ context.Context, region, _ string) (aws.Config, error) {
			return aws.Config{Region: region}, nil
		},
	}
}

func TestPutSecureParameterPutsThenTags(t *testing.T) {
	const name = "/fabrica/workstation/abc123def4567890/dcv-password"
	m := &mockParameter{
		putFn: func(_ context.Context, in *ssm.PutParameterInput) (*ssm.PutParameterOutput, error) {
			if aws.ToString(in.Name) != name {
				t.Errorf("name = %q", aws.ToString(in.Name))
			}
			if aws.ToString(in.Value) != "secret-value" {
				t.Errorf("value not stored")
			}
			if in.Type != ssmtypes.ParameterTypeSecureString {
				t.Errorf("type = %s", in.Type)
			}
			if in.Overwrite != nil {
				t.Error("Overwrite must be absent: a per-create name is never overwritten, and Overwrite+Tags are rejected together")
			}
			if len(in.Tags) != 0 {
				t.Errorf("Tags on PutParameter = %v; Parameter Store rejects Tags with Overwrite", in.Tags)
			}
			return &ssm.PutParameterOutput{}, nil
		},
		tagFn: func(_ context.Context, in *ssm.AddTagsToResourceInput) (*ssm.AddTagsToResourceOutput, error) {
			if in.ResourceType != ssmtypes.ResourceTypeForTaggingParameter {
				t.Errorf("resource type = %s", in.ResourceType)
			}
			got := map[string]string{}
			for _, tag := range in.Tags {
				got[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
			}
			if got["ManagedBy"] != "fabrica" {
				t.Errorf("tags = %v, want ManagedBy=fabrica", got)
			}
			if got["Project"] != "fabrica" {
				t.Errorf("tags = %v, want Project kept", got)
			}
			return &ssm.AddTagsToResourceOutput{}, nil
		},
	}
	p := testProviderWithParameter(m)
	err := p.PutSecureParameter(context.Background(), fabricac.SecureParameter{
		Name:  name,
		Value: "secret-value",
		Tags:  map[string]string{"ManagedBy": "someone-else", "Project": "fabrica"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.puts != 1 || m.tags != 1 || m.dels != 0 {
		t.Fatalf("puts=%d tags=%d dels=%d", m.puts, m.tags, m.dels)
	}
}

// TestPutSecureParameterFailsLoudWhenNameExists covers the collision paths:
// a typed ParameterExists error and a raw API error both fail with delete-the-
// orphan guidance, omit the secret, and never attempt a follow-up delete.
func TestPutSecureParameterFailsLoudWhenNameExists(t *testing.T) {
	const name = "/fabrica/workstation/abc123def4567890/dcv-password"
	for _, tc := range []struct {
		name   string
		putErr error
	}{
		{name: "typed ParameterAlreadyExists", putErr: &ssmtypes.ParameterAlreadyExists{Message: aws.String("exists")}},
		{name: "raw API ParameterAlreadyExists", putErr: paramAPIError{code: "ParameterAlreadyExists"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &mockParameter{putFn: func(context.Context, *ssm.PutParameterInput) (*ssm.PutParameterOutput, error) {
				return nil, tc.putErr
			}}
			p := testProviderWithParameter(m)
			err := p.PutSecureParameter(context.Background(), fabricac.SecureParameter{
				Name:  name,
				Value: "secret-value",
			})
			if err == nil || !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("err = %v, want already-exists error", err)
			}
			if !strings.Contains(err.Error(), "aws ssm delete-parameter") {
				t.Errorf("err missing cleanup guidance: %v", err)
			}
			if strings.Contains(err.Error(), "secret-value") {
				t.Fatal("error includes the secret")
			}
			if m.puts != 1 || m.tags != 0 || m.dels != 0 {
				t.Fatalf("puts=%d tags=%d dels=%d, want no follow-up calls", m.puts, m.tags, m.dels)
			}
		})
	}
}

func TestPutSecureParameterTagFailureDeletes(t *testing.T) {
	m := &mockParameter{
		tagFn: func(context.Context, *ssm.AddTagsToResourceInput) (*ssm.AddTagsToResourceOutput, error) {
			return nil, errors.New("tag denied")
		},
	}
	p := testProviderWithParameter(m)
	err := p.PutSecureParameter(context.Background(), fabricac.SecureParameter{
		Name:  "/fabrica/workstation/dcv-password",
		Value: "secret-value",
	})
	if err == nil || !strings.Contains(err.Error(), "tagging SSM parameter") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatal("error includes the secret")
	}
	if m.dels != 1 {
		t.Fatalf("cleanup deletes = %d, want 1", m.dels)
	}
}

func TestPutSecureParameterRejectsBadInput(t *testing.T) {
	p := testProviderWithParameter(&mockParameter{})
	if err := p.PutSecureParameter(context.Background(), fabricac.SecureParameter{Name: "flat", Value: "x"}); err == nil {
		t.Fatal("expected hierarchical-name error")
	}
	if err := p.PutSecureParameter(context.Background(), fabricac.SecureParameter{Name: "/fabrica/workstation/dcv-password", Value: ""}); err == nil {
		t.Fatal("expected empty-value error")
	}
}

func TestDeleteParameterNotFoundIsSuccess(t *testing.T) {
	m := &mockParameter{
		delFn: func(context.Context, *ssm.DeleteParameterInput) (*ssm.DeleteParameterOutput, error) {
			return nil, &ssmtypes.ParameterNotFound{Message: aws.String("gone")}
		},
	}
	p := testProviderWithParameter(m)
	if err := p.DeleteParameter(context.Background(), "/fabrica/workstation/dcv-password"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteParameterAPINotFoundIsSuccess(t *testing.T) {
	m := &mockParameter{
		delFn: func(context.Context, *ssm.DeleteParameterInput) (*ssm.DeleteParameterOutput, error) {
			return nil, paramAPIError{code: "ParameterNotFound"}
		},
	}
	p := testProviderWithParameter(m)
	if err := p.DeleteParameter(context.Background(), "/fabrica/workstation/dcv-password"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteParameterOtherError(t *testing.T) {
	m := &mockParameter{
		delFn: func(context.Context, *ssm.DeleteParameterInput) (*ssm.DeleteParameterOutput, error) {
			return nil, paramAPIError{code: "AccessDeniedException"}
		},
	}
	p := testProviderWithParameter(m)
	err := p.DeleteParameter(context.Background(), "/fabrica/workstation/dcv-password")
	if err == nil || !strings.Contains(err.Error(), "deleting SSM parameter") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteParameterEmptyName(t *testing.T) {
	p := testProviderWithParameter(&mockParameter{})
	if err := p.DeleteParameter(context.Background(), "  "); err == nil {
		t.Fatal("expected empty-name error")
	}
}

func TestPutSecureParameterStoreErrorOmitsSecret(t *testing.T) {
	m := &mockParameter{
		putFn: func(context.Context, *ssm.PutParameterInput) (*ssm.PutParameterOutput, error) {
			return nil, errors.New("access denied")
		},
	}
	p := testProviderWithParameter(m)
	err := p.PutSecureParameter(context.Background(), fabricac.SecureParameter{
		Name:  "/fabrica/workstation/dcv-password",
		Value: "secret-value",
	})
	if err == nil || !strings.Contains(err.Error(), "storing SSM parameter") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatal("error includes the secret")
	}
	if m.tags != 0 || m.dels != 0 {
		t.Fatalf("tags=%d dels=%d, want no follow-up calls", m.tags, m.dels)
	}
}

func TestPutSecureParameterTagAndCleanupFailure(t *testing.T) {
	m := &mockParameter{
		tagFn: func(context.Context, *ssm.AddTagsToResourceInput) (*ssm.AddTagsToResourceOutput, error) {
			return nil, errors.New("tag denied")
		},
		delFn: func(context.Context, *ssm.DeleteParameterInput) (*ssm.DeleteParameterOutput, error) {
			return nil, errors.New("delete denied")
		},
	}
	p := testProviderWithParameter(m)
	err := p.PutSecureParameter(context.Background(), fabricac.SecureParameter{
		Name:  "/fabrica/workstation/dcv-password",
		Value: "secret-value",
	})
	if err == nil || !strings.Contains(err.Error(), "cleanup also failed") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatal("error includes the secret")
	}
}

func TestParameterStoreConfigError(t *testing.T) {
	p := testProviderWithParameter(&mockParameter{})
	p.loadConfig = func(context.Context, string, string) (aws.Config, error) {
		return aws.Config{}, errors.New("no creds")
	}
	err := p.PutSecureParameter(context.Background(), fabricac.SecureParameter{
		Name:  "/fabrica/workstation/dcv-password",
		Value: "secret-value",
	})
	if err == nil || !strings.Contains(err.Error(), "no creds") {
		t.Fatalf("put err = %v", err)
	}
	if err := p.DeleteParameter(context.Background(), "/fabrica/workstation/dcv-password"); err == nil || !strings.Contains(err.Error(), "no creds") {
		t.Fatalf("delete err = %v", err)
	}
}

func TestPutSecureParameterRejectsEmptyName(t *testing.T) {
	p := testProviderWithParameter(&mockParameter{})
	if err := p.PutSecureParameter(context.Background(), fabricac.SecureParameter{Name: "  ", Value: "secret-value"}); err == nil {
		t.Fatal("expected empty-name error")
	}
}

type paramAPIError struct{ code string }

func (e paramAPIError) Error() string                 { return e.code }
func (e paramAPIError) ErrorCode() string             { return e.code }
func (e paramAPIError) ErrorMessage() string          { return e.code }
func (e paramAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }
