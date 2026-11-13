package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/tags"
)

var _ fabricac.StateBackendReconciler = (*awsProvider)(nil)

func (p *awsProvider) ReconcileStateBucket(ctx context.Context, bucket string, spec fabricac.StateBackendReconcileSpec) error {
	if !spec.Expected.Exists {
		return fmt.Errorf("reconciling S3 bucket %s: expected an existing resource; inspect the backend and repeat the full setup preflight", bucket)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return fmt.Errorf("reconciling S3 bucket %s: %w", bucket, err)
	}
	client := p.s3StateClient(cfg)
	actual, err := readStateBucketMetadata(ctx, client, bucket)
	if err != nil {
		return err
	}
	merged, err := revalidatedBackendTags(actual, spec)
	if err != nil {
		return fmt.Errorf("reconciling S3 bucket %s: %w", bucket, err)
	}
	return p.configureBucket(ctx, client, bucket, merged)
}

func (p *awsProvider) ReconcileStateLockTable(ctx context.Context, table string, spec fabricac.StateBackendReconcileSpec) error {
	if !spec.Expected.Exists {
		return fmt.Errorf("reconciling DynamoDB table %s: expected an existing resource; inspect the backend and repeat the full setup preflight", table)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return fmt.Errorf("reconciling DynamoDB table %s: %w", table, err)
	}
	client := p.dynamoDBStateClient(cfg)
	actual, arn, err := readStateLockTableMetadata(ctx, client, table)
	if err != nil {
		return err
	}
	merged, err := revalidatedBackendTags(actual, spec)
	if err != nil {
		return fmt.Errorf("reconciling DynamoDB table %s: %w", table, err)
	}
	if _, err := client.TagResource(ctx, &dynamodb.TagResourceInput{ResourceArn: aws.String(arn), Tags: dynamoTagList(merged)}); err != nil {
		return fmt.Errorf("tagging DynamoDB table %s: %w — check dynamodb:TagResource permission", table, err)
	}
	return nil
}

// Compare raw values and presence, not ownership policy. Only the caller's
// classifier can approve historical repair or same-owner reconciliation.
func revalidatedBackendTags(actual fabricac.StateBackendMetadata, spec fabricac.StateBackendReconcileSpec) (map[string]string, error) {
	if !actual.Exists {
		return nil, fmt.Errorf("resource disappeared; inspect the backend and repeat the full setup preflight")
	}
	for _, key := range []string{tags.ManagedByKey, tags.OwnershipVersionKey} {
		expectedValue, expectedPresent := spec.Expected.Tags[key]
		actualValue, actualPresent := actual.Tags[key]
		if expectedPresent != actualPresent || expectedValue != actualValue {
			return nil, fmt.Errorf("protected backend tag %s changed; inspect the backend and repeat the full setup preflight", key)
		}
	}
	marker, present := actual.Tags[tags.OwnershipVersionKey]
	if desired, supplied := spec.Tags[tags.OwnershipVersionKey]; supplied && (!present || desired != marker) {
		return nil, fmt.Errorf("existing backend marker must be preserved; inspect the backend and repeat the full setup preflight")
	}
	merged := make(map[string]string)
	for k, v := range actual.Tags {
		merged[k] = v
	}
	for k, v := range spec.Tags {
		merged[k] = v
	}
	return merged, nil
}
