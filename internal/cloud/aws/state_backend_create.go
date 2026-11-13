package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/tags"
)

var _ fabricac.StateBackendCreator = (*awsProvider)(nil)

func (p *awsProvider) CreateStateBucket(ctx context.Context, bucket string, spec fabricac.StateBucketCreateSpec) (fabricac.StateBackendCreateResult, error) {
	result := fabricac.StateBackendCreateResult{Identifier: bucket}
	if spec.StateDocumentKey == "" {
		return result, fmt.Errorf("creating S3 bucket %s: canonical document key is required; repeat the full setup preflight", bucket)
	}
	if err := validateBackendCreateTags(spec.Tags); err != nil {
		return result, fmt.Errorf("creating S3 bucket %s: %w", bucket, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return result, fmt.Errorf("creating S3 bucket %s: %w", bucket, err)
	}
	client := p.s3StateClient(cfg)
	before, err := readStateBucketMetadata(ctx, client, bucket)
	if err != nil {
		return result, err
	}
	if before.Exists {
		return result, fmt.Errorf("creating S3 bucket %s: resource appeared; inspect the backend and repeat the full setup preflight", bucket)
	}
	if err := p.createBucket(ctx, client, bucket, spec.Region); err != nil {
		return result, err
	}
	result.Created = true
	after, err := readStateBucketMetadata(ctx, client, bucket)
	if err != nil {
		return result, err
	}
	if err := validateCreatedBackendTags(after, spec.Tags, false); err != nil {
		return result, fmt.Errorf("checking created S3 bucket %s: %w", bucket, err)
	}
	_, err = p.GetStateDocument(ctx, bucket, spec.StateDocumentKey)
	if err == nil {
		return result, fmt.Errorf("checking created S3 bucket %s: canonical document appeared; inspect the backend and repeat the full setup preflight", bucket)
	}
	if !errors.Is(err, fabricac.ErrStateDocumentMissing) {
		return result, fmt.Errorf("checking created S3 bucket %s canonical document: %w", bucket, err)
	}
	// GetStateDocument also maps NoSuchBucket to missing. Recheck both presence
	// and protected tags; completion eligibility exists only in this invocation.
	final, err := readStateBucketMetadata(ctx, client, bucket)
	if err != nil {
		return result, err
	}
	if err := validateBackendSnapshot(final, after); err != nil {
		return result, fmt.Errorf("checking created S3 bucket %s: %w", bucket, err)
	}
	if err := validateCreatedBackendTags(final, spec.Tags, false); err != nil {
		return result, fmt.Errorf("checking created S3 bucket %s: %w", bucket, err)
	}
	if err := p.configureBucket(ctx, client, bucket, mergeBackendTags(final.Tags, spec.Tags)); err != nil {
		return result, err
	}
	return result, nil
}

func (p *awsProvider) CreateStateLockTable(ctx context.Context, table string, desired map[string]string) (fabricac.StateBackendCreateResult, error) {
	result := fabricac.StateBackendCreateResult{Identifier: table}
	if err := validateBackendCreateTags(desired); err != nil {
		return result, fmt.Errorf("creating DynamoDB table %s: %w", table, err)
	}
	ctx, cancel := context.WithTimeout(ctx, stateBackendDeleteTimeout)
	defer cancel()
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return result, fmt.Errorf("creating DynamoDB table %s: %w", table, err)
	}
	client := p.dynamoDBStateClient(cfg)
	before, _, err := readStateLockTableMetadata(ctx, client, table)
	if err != nil {
		return result, err
	}
	if before.Exists {
		return result, fmt.Errorf("creating DynamoDB table %s: resource appeared; inspect the backend and repeat the full setup preflight", table)
	}
	if err := createLockTable(ctx, client, table, desired); err != nil {
		return result, err
	}
	result.Created = true
	if err := p.tableExistsWaiter(client).Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}, 2*time.Minute); err != nil {
		return result, fmt.Errorf("waiting for DynamoDB table %s to become active: %w", table, err)
	}
	actual, _, err := readStateLockTableMetadata(ctx, client, table)
	if err != nil {
		return result, err
	}
	if err := validateCreatedBackendTags(actual, desired, true); err != nil {
		return result, fmt.Errorf("checking created DynamoDB table %s: %w", table, err)
	}
	return result, nil
}

func validateBackendCreateTags(desired map[string]string) error {
	owner := desired[tags.ManagedByKey]
	if owner == "" {
		return fmt.Errorf("trusted ManagedBy tag is required; repeat the full setup preflight")
	}
	identity, err := tags.ResolveIdentity(owner)
	if err != nil {
		return err
	}
	for key, value := range identity.Standard("setup", desired["FabricaVersion"]) {
		if value == "" || desired[key] != value {
			return fmt.Errorf("trusted setup tag %s is required; repeat the full setup preflight", key)
		}
	}
	marker, present := desired[tags.OwnershipVersionKey]
	if owner == tags.ManagedByValue && present || owner != tags.ManagedByValue && (!present || marker != "1") {
		return fmt.Errorf("trusted ownership marker must be absent for default creation and 1 for custom creation; repeat the full setup preflight")
	}
	return nil
}

// S3 may still be untagged after this invocation's successful create request.
// DynamoDB must return every protected tag supplied atomically on CreateTable.
func validateCreatedBackendTags(actual fabricac.StateBackendMetadata, desired map[string]string, required bool) error {
	if !actual.Exists {
		return fmt.Errorf("created resource disappeared; inspect the backend and repeat the full setup preflight")
	}
	for key, value := range actual.Tags {
		if !tags.IdentityKey(key) {
			continue
		}
		want, present := desired[key]
		if !present || value != want {
			return fmt.Errorf("created resource has conflicting protected tag %s; inspect the backend and repeat the full setup preflight", key)
		}
	}
	if !required {
		return nil
	}
	for key, value := range desired {
		if !tags.IdentityKey(key) {
			continue
		}
		got, present := actual.Tags[key]
		if !present || got != value {
			return fmt.Errorf("created resource lacks protected request tag %s; inspect the backend and repeat the full setup preflight", key)
		}
	}
	return nil
}
