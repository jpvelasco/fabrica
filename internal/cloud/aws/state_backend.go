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

func (p *awsProvider) StateBucketExists(ctx context.Context, bucket string) (bool, error) {
	cfg, err := loadAWSConfig(ctx, p.awsCfg.region, p.awsCfg.profile)
	if err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := s3.NewFromConfig(cfg)
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
	cfg, err := loadAWSConfig(ctx, p.awsCfg.region, p.awsCfg.profile)
	if err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client := dynamodb.NewFromConfig(cfg)
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
	cfg, err := loadAWSConfig(ctx, p.awsCfg.region, p.awsCfg.profile)
	if err != nil {
		return result, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := s3.NewFromConfig(cfg)
	_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "404", "NoSuchBucket":
				result.Missing = true
				return result, nil
			case "BucketNotEmpty":
				return result, fmt.Errorf("deleting S3 bucket %s: bucket is not empty; empty it manually before running destroy again: %w", bucket, err)
			}
		}
		return result, fmt.Errorf("deleting S3 bucket %s: %w", bucket, err)
	}

	result.Deleted = true
	return result, nil
}

func (p *awsProvider) DeleteStateLockTable(ctx context.Context, table string) (fabricac.StateBackendDeleteResult, error) {
	result := fabricac.StateBackendDeleteResult{Identifier: table}
	cfg, err := loadAWSConfig(ctx, p.awsCfg.region, p.awsCfg.profile)
	if err != nil {
		return result, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client := dynamodb.NewFromConfig(cfg)
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

	result.Deleted = true
	return result, nil
}
