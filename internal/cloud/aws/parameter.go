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
)

// Parameter Store rejects Tags together with Overwrite, so a re-create of a
// stale parameter writes the value first and tags second.
var _ fabricac.ParameterStore = (*awsProvider)(nil)

type parameterClient interface {
	PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	DeleteParameter(ctx context.Context, params *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
	AddTagsToResource(ctx context.Context, params *ssm.AddTagsToResourceInput, optFns ...func(*ssm.Options)) (*ssm.AddTagsToResourceOutput, error)
}

type parameterClientFactory func(aws.Config) parameterClient

func (p *awsProvider) parameterClient(cfg aws.Config) parameterClient {
	if p.newParameterClient != nil {
		return p.newParameterClient(cfg)
	}
	return ssm.NewFromConfig(cfg)
}

// PutSecureParameter stores value as a SecureString. Overwrite is set so a
// parameter left by a crashed create is replaced. The value is never included
// in an error string.
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
		Name:      aws.String(name),
		Value:     aws.String(param.Value),
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
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
	tags := []ssmtypes.Tag{{
		Key:   aws.String("ManagedBy"),
		Value: aws.String("fabrica"),
	}}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		if k == "" || k == "ManagedBy" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		tags = append(tags, ssmtypes.Tag{Key: aws.String(k), Value: aws.String(extra[k])})
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
