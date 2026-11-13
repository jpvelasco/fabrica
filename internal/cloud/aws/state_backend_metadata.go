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

var _ fabricac.StateBackendMetadataReader = (*awsProvider)(nil)

func (p *awsProvider) StateBucketMetadata(ctx context.Context, bucket string) (fabricac.StateBackendMetadata, error) {
	empty := fabricac.StateBackendMetadata{}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return empty, fmt.Errorf("reading S3 bucket %s metadata: %w", bucket, err)
	}
	return readStateBucketMetadata(ctx, p.s3StateClient(cfg), bucket)
}

func readStateBucketMetadata(ctx context.Context, client stateBackendS3Client, bucket string) (fabricac.StateBackendMetadata, error) {
	empty := fabricac.StateBackendMetadata{}
	out, err := client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String(bucket)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "NoSuchBucket":
				return empty, nil
			case "NoSuchTagSet":
				return fabricac.StateBackendMetadata{Exists: true, Tags: map[string]string{}}, nil
			}
		}
		return empty, fmt.Errorf("reading S3 bucket %s metadata: %w — check s3:GetBucketTagging permission", bucket, err)
	}
	if out == nil {
		return empty, fmt.Errorf("reading S3 bucket %s metadata: GetBucketTagging returned no output; inspect the backend before retrying", bucket)
	}
	tags := map[string]string{}
	for _, tag := range out.TagSet {
		if err := addBackendMetadataTag(tags, tag.Key, tag.Value); err != nil {
			return empty, fmt.Errorf("reading S3 bucket %s metadata: %w", bucket, err)
		}
	}
	return fabricac.StateBackendMetadata{Exists: true, Tags: tags}, nil
}

func (p *awsProvider) StateLockTableMetadata(ctx context.Context, table string) (fabricac.StateBackendMetadata, error) {
	empty := fabricac.StateBackendMetadata{}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return empty, fmt.Errorf("reading DynamoDB table %s metadata: %w", table, err)
	}
	metadata, _, err := readStateLockTableMetadata(ctx, p.dynamoDBStateClient(cfg), table)
	return metadata, err
}

func readStateLockTableMetadata(ctx context.Context, client stateBackendDynamoDBClient, table string) (fabricac.StateBackendMetadata, string, error) {
	empty := fabricac.StateBackendMetadata{}
	out, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "ResourceNotFoundException" {
			return empty, "", nil
		}
		return empty, "", fmt.Errorf("reading DynamoDB table %s metadata: DescribeTable: %w — check dynamodb:DescribeTable permission", table, err)
	}
	if out == nil || out.Table == nil || aws.ToString(out.Table.TableArn) == "" {
		return empty, "", fmt.Errorf("reading DynamoDB table %s metadata: DescribeTable returned no table ARN; inspect the backend before retrying", table)
	}
	input := &dynamodb.ListTagsOfResourceInput{ResourceArn: out.Table.TableArn}
	tags := map[string]string{}
	seenTokens := map[string]bool{}
	for {
		page, err := client.ListTagsOfResource(ctx, input)
		if err != nil {
			return empty, "", fmt.Errorf("reading DynamoDB table %s metadata: ListTagsOfResource: %w — check dynamodb:ListTagsOfResource permission", table, err)
		}
		if page == nil {
			return empty, "", fmt.Errorf("reading DynamoDB table %s metadata: ListTagsOfResource returned no output; inspect the backend before retrying", table)
		}
		for _, tag := range page.Tags {
			if err := addBackendMetadataTag(tags, tag.Key, tag.Value); err != nil {
				return empty, "", fmt.Errorf("reading DynamoDB table %s metadata: %w", table, err)
			}
		}
		token := aws.ToString(page.NextToken)
		if token == "" {
			return fabricac.StateBackendMetadata{Exists: true, Tags: tags}, aws.ToString(out.Table.TableArn), nil
		}
		if seenTokens[token] {
			return empty, "", fmt.Errorf("reading DynamoDB table %s metadata: repeated pagination token; inspect the backend before retrying", table)
		}
		seenTokens[token] = true
		input.NextToken = page.NextToken
	}
}

// Both APIs require tag keys and values. Reject duplicates rather than choosing
// an arbitrary ownership value from an ambiguous or inconsistent response.
func addBackendMetadataTag(tags map[string]string, key, value *string) error {
	if key == nil || *key == "" || value == nil {
		return fmt.Errorf("incomplete tag returned; inspect the backend before retrying")
	}
	if _, exists := tags[*key]; exists {
		return fmt.Errorf("duplicate tag key returned; inspect the backend before retrying")
	}
	tags[*key] = *value
	return nil
}
