package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.StateBackendChecker = (*awsProvider)(nil)
var _ fabricac.StateBackendDestroyer = (*awsProvider)(nil)

type stateBackendConfigLoader func(context.Context, string, string) (aws.Config, error)

type stateBackendS3Client interface {
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	DeleteBucket(context.Context, *s3.DeleteBucketInput, ...func(*s3.Options)) (*s3.DeleteBucketOutput, error)
	CreateBucket(context.Context, *s3.CreateBucketInput, ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	PutBucketVersioning(context.Context, *s3.PutBucketVersioningInput, ...func(*s3.Options)) (*s3.PutBucketVersioningOutput, error)
	PutBucketEncryption(context.Context, *s3.PutBucketEncryptionInput, ...func(*s3.Options)) (*s3.PutBucketEncryptionOutput, error)
	PutPublicAccessBlock(context.Context, *s3.PutPublicAccessBlockInput, ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error)
}

type stateBackendDynamoDBClient interface {
	DescribeTable(context.Context, *dynamodb.DescribeTableInput, ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error)
	DeleteTable(context.Context, *dynamodb.DeleteTableInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteTableOutput, error)
	CreateTable(context.Context, *dynamodb.CreateTableInput, ...func(*dynamodb.Options)) (*dynamodb.CreateTableOutput, error)
}

type stateBackendBucketWaiter interface {
	Wait(context.Context, *s3.HeadBucketInput, time.Duration, ...func(*s3.BucketNotExistsWaiterOptions)) error
}

type stateBackendTableWaiter interface {
	Wait(context.Context, *dynamodb.DescribeTableInput, time.Duration, ...func(*dynamodb.TableNotExistsWaiterOptions)) error
}

type stateBackendTableExistsWaiter interface {
	Wait(context.Context, *dynamodb.DescribeTableInput, time.Duration, ...func(*dynamodb.TableExistsWaiterOptions)) error
}

type stateBackendS3ClientFactory func(aws.Config) stateBackendS3Client
type stateBackendDynamoDBClientFactory func(aws.Config) stateBackendDynamoDBClient
type stateBackendBucketWaiterFactory func(s3.HeadBucketAPIClient) stateBackendBucketWaiter
type stateBackendTableWaiterFactory func(dynamodb.DescribeTableAPIClient) stateBackendTableWaiter
type stateBackendTableExistsWaiterFactory func(dynamodb.DescribeTableAPIClient) stateBackendTableExistsWaiter

func (p *awsProvider) StateBucketExists(ctx context.Context, bucket string) (bool, error) {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := p.s3StateClient(cfg)
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "404" || apiErr.ErrorCode() == "NoSuchBucket" {
				return false, nil
			}
		}
		return false, fmt.Errorf("checking S3 bucket %s: %w", bucket, err)
	}

	return true, nil
}

func (p *awsProvider) StateLockTableExists(ctx context.Context, table string) (bool, error) {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := p.dynamoDBStateClient(cfg)
	_, err = client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "ResourceNotFoundException" {
				return false, nil
			}
		}
		return false, fmt.Errorf("checking DynamoDB table %s: %w", table, err)
	}

	return true, nil
}

func (p *awsProvider) DeleteStateBucket(ctx context.Context, bucket string) (fabricac.StateBackendDeleteResult, error) {
	result := fabricac.StateBackendDeleteResult{Identifier: bucket}
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return result, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := p.s3StateClient(cfg)
	_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "404", "NoSuchBucket":
				result.Missing = true
				return result, nil
			case "BucketNotEmpty":
				return result, fmt.Errorf("deleting S3 bucket %s: %w", bucket, fabricac.ErrStateBucketNotEmpty)
			}
		}
		return result, fmt.Errorf("deleting S3 bucket %s: %w", bucket, err)
	}

	waiter := p.bucketNotExistsWaiter(client)
	if err := waiter.Wait(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}, 2*time.Minute); err != nil {
		return result, fmt.Errorf("waiting for S3 bucket %s deletion: %w", bucket, err)
	}

	result.Deleted = true
	return result, nil
}

func (p *awsProvider) DeleteStateLockTable(ctx context.Context, table string) (fabricac.StateBackendDeleteResult, error) {
	result := fabricac.StateBackendDeleteResult{Identifier: table}
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return result, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := p.dynamoDBStateClient(cfg)
	_, err = client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			if apiErr.ErrorCode() == "ResourceNotFoundException" {
				result.Missing = true
				return result, nil
			}
		}
		return result, fmt.Errorf("deleting DynamoDB table %s: %w", table, err)
	}

	waiter := p.tableNotExistsWaiter(client)
	if err := waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}, 2*time.Minute); err != nil {
		return result, fmt.Errorf("waiting for DynamoDB table %s deletion: %w", table, err)
	}

	result.Deleted = true
	return result, nil
}

func (p *awsProvider) EnsureStateBucket(ctx context.Context, bucket, region string) (fabricac.StateBackendCreateResult, error) {
	result := fabricac.StateBackendCreateResult{Identifier: bucket}
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return result, err
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	client := p.s3StateClient(cfg)

	exists, err := p.bucketExists(ctx, client, bucket)
	if err != nil {
		return result, err
	}
	if !exists {
		if err := p.createBucket(ctx, client, bucket, region); err != nil {
			return result, err
		}
		result.Created = true
	}

	// Reconcile configuration on every run so re-running setup heals drift.
	if err := p.configureBucket(ctx, client, bucket); err != nil {
		return result, err
	}
	return result, nil
}

func (p *awsProvider) bucketExists(ctx context.Context, client stateBackendS3Client, bucket string) (bool, error) {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return true, nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "404", "NoSuchBucket", "NotFound":
			return false, nil
		}
	}
	return false, fmt.Errorf("checking S3 bucket %s: %w", bucket, err)
}

func (p *awsProvider) createBucket(ctx context.Context, client stateBackendS3Client, bucket, region string) error {
	in := &s3.CreateBucketInput{Bucket: aws.String(bucket)}
	// us-east-1 must omit the location constraint; every other region requires it.
	if region != "" && region != "us-east-1" {
		in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}
	if _, err := client.CreateBucket(ctx, in); err != nil {
		return fmt.Errorf("creating S3 bucket %s: %w — check the name is globally unique and you have s3:CreateBucket", bucket, err)
	}
	return nil
}

func (p *awsProvider) configureBucket(ctx context.Context, client stateBackendS3Client, bucket string) error {
	if _, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket:                  aws.String(bucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{Status: s3types.BucketVersioningStatusEnabled},
	}); err != nil {
		return fmt.Errorf("enabling versioning on S3 bucket %s: %w", bucket, err)
	}

	rule := s3types.ServerSideEncryptionRule{
		ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
			SSEAlgorithm: s3types.ServerSideEncryptionAes256,
		},
	}
	if p.cfg != nil && p.cfg.State.KMSKeyID != "" {
		rule.ApplyServerSideEncryptionByDefault.SSEAlgorithm = s3types.ServerSideEncryptionAwsKms
		rule.ApplyServerSideEncryptionByDefault.KMSMasterKeyID = aws.String(p.cfg.State.KMSKeyID)
	}
	if _, err := client.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
		Bucket:                            aws.String(bucket),
		ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{Rules: []s3types.ServerSideEncryptionRule{rule}},
	}); err != nil {
		return fmt.Errorf("enabling encryption on S3 bucket %s: %w", bucket, err)
	}

	if _, err := client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: aws.String(bucket),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       aws.Bool(true),
			BlockPublicPolicy:     aws.Bool(true),
			IgnorePublicAcls:      aws.Bool(true),
			RestrictPublicBuckets: aws.Bool(true),
		},
	}); err != nil {
		return fmt.Errorf("blocking public access on S3 bucket %s: %w", bucket, err)
	}
	return nil
}

func (p *awsProvider) EnsureStateLockTable(ctx context.Context, table string) (fabricac.StateBackendCreateResult, error) {
	result := fabricac.StateBackendCreateResult{Identifier: table}
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return result, err
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	client := p.dynamoDBStateClient(cfg)

	_, err = client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if err == nil {
		return result, nil // already exists
	}
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ResourceNotFoundException" {
		return result, fmt.Errorf("checking DynamoDB table %s: %w", table, err)
	}

	if _, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String(table),
		BillingMode: dynamodbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []dynamodbtypes.AttributeDefinition{
			{AttributeName: aws.String("LockID"), AttributeType: dynamodbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []dynamodbtypes.KeySchemaElement{
			{AttributeName: aws.String("LockID"), KeyType: dynamodbtypes.KeyTypeHash},
		},
	}); err != nil {
		return result, fmt.Errorf("creating DynamoDB table %s: %w — check the dynamodb:CreateTable permission", table, err)
	}

	waiter := p.tableExistsWaiter(client)
	if err := waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}, 2*time.Minute); err != nil {
		return result, fmt.Errorf("waiting for DynamoDB table %s to become active: %w", table, err)
	}
	result.Created = true
	return result, nil
}

func (p *awsProvider) tableExistsWaiter(client dynamodb.DescribeTableAPIClient) stateBackendTableExistsWaiter {
	if p.newTableExistsWaiter != nil {
		return p.newTableExistsWaiter(client)
	}
	return dynamodb.NewTableExistsWaiter(client)
}

func (p *awsProvider) stateBackendConfig(ctx context.Context) (aws.Config, error) {
	load := p.loadConfig
	if load == nil {
		load = loadAWSConfig
	}
	return load(ctx, p.awsCfg.region, p.awsCfg.profile)
}

func (p *awsProvider) s3StateClient(cfg aws.Config) stateBackendS3Client {
	if p.newS3StateClient != nil {
		return p.newS3StateClient(cfg)
	}
	return s3.NewFromConfig(cfg)
}

func (p *awsProvider) dynamoDBStateClient(cfg aws.Config) stateBackendDynamoDBClient {
	if p.newDynamoDBStateClient != nil {
		return p.newDynamoDBStateClient(cfg)
	}
	return dynamodb.NewFromConfig(cfg)
}

func (p *awsProvider) bucketNotExistsWaiter(client s3.HeadBucketAPIClient) stateBackendBucketWaiter {
	if p.newBucketNotExistsWaiter != nil {
		return p.newBucketNotExistsWaiter(client)
	}
	return s3.NewBucketNotExistsWaiter(client)
}

func (p *awsProvider) tableNotExistsWaiter(client dynamodb.DescribeTableAPIClient) stateBackendTableWaiter {
	if p.newTableNotExistsWaiter != nil {
		return p.newTableNotExistsWaiter(client)
	}
	return dynamodb.NewTableNotExistsWaiter(client)
}
