package aws

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/smithy-go"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	fabricatags "github.com/jpvelasco/fabrica/internal/tags"
)

// Parameter Store rejects Tags together with Overwrite, so a re-create of a
// stale parameter writes the value first and tags second.
//
// PutSecureParameter never overwrites: each create mints its own per-create
// parameter name (workstation.SessionPasswordParameterFor), so a successful
// put implies the name was free. A pre-existing name is a collision (a crash
// left an orphan or two creates raced on one nonce) and fails loudly so the
// operator can delete the orphan via AWS Console/CLI — never a blind
// overwrite of a live instance's secret.
var (
	_ fabricac.ParameterStore        = (*awsProvider)(nil)
	_ fabricac.SecureParameterReader = (*awsProvider)(nil)
)

type parameterClient interface {
	PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	DeleteParameter(ctx context.Context, params *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
	AddTagsToResource(ctx context.Context, params *ssm.AddTagsToResourceInput, optFns ...func(*ssm.Options)) (*ssm.AddTagsToResourceOutput, error)
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

type parameterClientFactory func(aws.Config) parameterClient

func (p *awsProvider) parameterClient(cfg aws.Config) parameterClient {
	if p.newParameterClient != nil {
		return p.newParameterClient(cfg)
	}
	return ssm.NewFromConfig(cfg)
}

// PutSecureParameter stores value as a SecureString without overwriting.
// ParameterExists is an error with cleanup guidance; the value is never
// included in an error string.
func (p *awsProvider) PutSecureParameter(ctx context.Context, param fabricac.SecureParameter) error {
	name := strings.TrimSpace(param.Name)
	if name == "" || !strings.HasPrefix(name, "/") {
		return fmt.Errorf("SSM parameter name must be a non-empty hierarchical name starting with /")
	}
	if strings.TrimSpace(param.Value) == "" {
		return fmt.Errorf("SSM parameter %s value must not be empty", name)
	}
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return err
	}
	client := p.parameterClient(cfg)
	_, err = client.PutParameter(ctx, &ssm.PutParameterInput{
		Name:  aws.String(name),
		Value: aws.String(param.Value),
		Type:  ssmtypes.ParameterTypeSecureString,
	})
	if err != nil {
		if isParameterExists(err) {
			return fmt.Errorf("SSM parameter %s already exists: a previous create left it behind or a concurrent create raced for the same name; delete the orphan with `aws ssm delete-parameter --name %s` after confirming no live instance uses it, then re-run", name, name)
		}
		return fmt.Errorf("storing SSM parameter %s: %w", name, err)
	}
	tags := parameterTags(param.Tags)
	_, err = client.AddTagsToResource(ctx, &ssm.AddTagsToResourceInput{
		ResourceType: ssmtypes.ResourceTypeForTaggingParameter,
		ResourceId:   aws.String(name),
		Tags:         tags,
	})
	if err != nil {
		// The value is already stored. Drop it so a failed tag write does
		// not leave an untagged secret behind for the caller to miss.
		_, delErr := client.DeleteParameter(ctx, &ssm.DeleteParameterInput{Name: aws.String(name)})
		if delErr != nil && !isParameterNotFound(delErr) {
			return fmt.Errorf("tagging SSM parameter %s: %w (cleanup also failed: %v)", name, err, delErr)
		}
		return fmt.Errorf("tagging SSM parameter %s: %w", name, err)
	}
	return nil
}

// GetSecureParameter returns the decrypted SecureString value. The value is
// never included in an error string.
func (p *awsProvider) GetSecureParameter(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("SSM parameter name must be a non-empty hierarchical name starting with /")
	}
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return "", err
	}
	out, err := p.parameterClient(cfg).GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		if isParameterNotFound(err) {
			return "", fmt.Errorf("SSM parameter %s: %w", name, fabricac.ErrParameterNotFound)
		}
		return "", fmt.Errorf("reading SSM parameter %s: %w", name, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("SSM parameter %s returned no value", name)
	}
	return *out.Parameter.Value, nil
}

// DeleteParameter removes the SecureString. ParameterNotFound is success.
func (p *awsProvider) DeleteParameter(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("SSM parameter name must not be empty")
	}
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return err
	}
	_, err = p.parameterClient(cfg).DeleteParameter(ctx, &ssm.DeleteParameterInput{
		Name: aws.String(name),
	})
	if err != nil && !isParameterNotFound(err) {
		return fmt.Errorf("deleting SSM parameter %s: %w", name, err)
	}
	return nil
}

func parameterTags(extra map[string]string) []ssmtypes.Tag {
	merged := (fabricatags.Identity{}).MergeProtected(nil, extra)
	tags := []ssmtypes.Tag{{
		Key:   aws.String(fabricatags.ManagedByKey),
		Value: aws.String(merged[fabricatags.ManagedByKey]),
	}}
	keys := make([]string, 0, len(merged)-1)
	for k := range merged {
		if k == fabricatags.ManagedByKey {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		tags = append(tags, ssmtypes.Tag{Key: aws.String(k), Value: aws.String(merged[k])})
	}
	return tags
}

func isParameterNotFound(err error) bool {
	var notFound *ssmtypes.ParameterNotFound
	if errors.As(err, &notFound) {
		return true
	}
	var api smithy.APIError
	return errors.As(err, &api) && api.ErrorCode() == "ParameterNotFound"
}

func isParameterExists(err error) bool {
	var exists *ssmtypes.ParameterAlreadyExists
	if errors.As(err, &exists) {
		return true
	}
	var api smithy.APIError
	return errors.As(err, &api) && api.ErrorCode() == "ParameterAlreadyExists"
}
