package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.StateBackendChecker = (*awsProvider)(nil)
var _ fabricac.StateBackendDestroyer = (*awsProvider)(nil)

type stateBackendConfigLoader func(context.Context, string, string) (aws.Config, error)

type stateBackendS3Client interface {
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	DeleteBucket(context.Context, *s3.DeleteBucketInput, ...func(*s3.Options)) (*s3.DeleteBucketOutput, error)
}

type stateBackendDynamoDBClient interface {
	DescribeTable(context.Context, *dynamodb.DescribeTableInput, ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error)
	DeleteTable(context.Context, *dynamodb.DeleteTableInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteTableOutput, error)
}

type stateBackendBucketWaiter interface {
	Wait(context.Context, *s3.HeadBucketInput, time.Duration, ...func(*s3.BucketNotExistsWaiterOptions)) error
}

type stateBackendTableWaiter interface {
	Wait(context.Context, *dynamodb.DescribeTableInput, time.Duration, ...func(*dynamodb.TableNotExistsWaiterOptions)) error
}

type stateBackendS3ClientFactory func(aws.Config) stateBackendS3Client
type stateBackendDynamoDBClientFactory func(aws.Config) stateBackendDynamoDBClient
type stateBackendBucketWaiterFactory func(s3.HeadBucketAPIClient) stateBackendBucketWaiter
type stateBackendTableWaiterFactory func(dynamodb.DescribeTableAPIClient) stateBackendTableWaiter

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
