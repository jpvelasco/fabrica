package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.StateLockManager = (*awsProvider)(nil)

// lockDynamoClient is the minimal DynamoDB surface for state-lock rows.
type lockDynamoClient interface {
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

type lockDynamoClientFactory func(aws.Config) lockDynamoClient

// AcquireStateLockRow performs the conditional put backing state.LockStore.
// A ConditionalCheckFailedException maps to cloud.ErrLockHeld so callers can
// branch with errors.Is.
func (p *awsProvider) AcquireStateLockRow(ctx context.Context, table string, item map[string]string, condition string, condValues map[string]string) error {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return err
	}

	attrs := make(map[string]dynamodbtypes.AttributeValue, len(item))
	for k, v := range item {
		attrs[k] = &dynamodbtypes.AttributeValueMemberS{Value: v}
	}
	exprValues := make(map[string]dynamodbtypes.AttributeValue, len(condValues))
	for k, v := range condValues {
		exprValues[k] = &dynamodbtypes.AttributeValueMemberS{Value: v}
	}

	client := p.lockDynamo(cfg)
	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:                 aws.String(table),
		Item:                      attrs,
		ConditionExpression:       aws.String(condition),
		ExpressionAttributeValues: exprValues,
	})
	if err != nil {
		var ccfe *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccfe) {
			return fabricac.ErrLockHeld
		}
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(err, &rnf) {
			return fabricac.ErrLockTableMissing
		}
		return fmt.Errorf("putting state lock row in %s: %w", table, err)
	}
	return nil
}

// ReleaseStateLockRow deletes the lock row conditioned on the caller's token.
// ConditionalCheckFailed maps to ErrLockHeld (another holder took over after
// the TTL lapsed — the caller logs and moves on).
func (p *awsProvider) ReleaseStateLockRow(ctx context.Context, table, lockID, token string) error {
	cfg, err := p.stateBackendConfig(ctx)
	if err != nil {
		return err
	}

	client := p.lockDynamo(cfg)
	_, err = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(table),
		Key: map[string]dynamodbtypes.AttributeValue{
			"LockID": &dynamodbtypes.AttributeValueMemberS{Value: lockID},
		},
		ConditionExpression:       aws.String("Token = :token"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{":token": &dynamodbtypes.AttributeValueMemberS{Value: token}},
	})
	if err != nil {
		var ccfe *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccfe) {
			return fabricac.ErrLockHeld
		}
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(err, &rnf) {
			return fabricac.ErrLockTableMissing
		}
		return fmt.Errorf("deleting state lock row from %s: %w", table, err)
	}
	return nil
}

func (p *awsProvider) lockDynamo(cfg aws.Config) lockDynamoClient {
	if p.newLockDynamoClient != nil {
		return p.newLockDynamoClient(cfg)
	}
	return dynamodb.NewFromConfig(cfg)
}
