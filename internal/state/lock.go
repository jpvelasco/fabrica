package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// LockStore is the DynamoDB-backed distributed lock.
type LockStore struct {
	table   string
	region  string
	client  lockClient
}

// lockClient is the minimal DynamoDB interface for lock operations
// (kept inside state package so it can be mocked in tests).
type lockClient interface {
	putItem(ctx context.Context, in *putInput) (*putOutput, error)
	deleteItem(ctx context.Context, in *deleteInput) (*deleteOutput, error)
}

// putInput / putOutput match the DynamoDB PutItem request/response shape.
type putInput struct {
	TableName        string
	Item             map[string]attrValue
	ConditionExpression string
	ExpressionAttributeValues map[string]attrValue
}

type putOutput struct{}

type deleteInput struct {
	TableName             string
	Key                   map[string]attrValue
	ConditionExpression  string
	ExpressionAttributeValues map[string]attrValue
}

type deleteOutput struct{}

type attrValue struct {
	S  string `json:"s,omitempty"`
	N  string `json:"n,omitempty"`
	B  []byte `json:"b,omitempty"`
	M  map[string]attrValue `json:"m,omitempty"`
}

// NewLockStore creates a new lock store for the given table and region.
func NewLockStore(table, region string, lc lockClient) *LockStore {
	return &LockStore{
		table:  table,
		region: region,
		client: lc,
	}
}

// Acquire attempts to acquire a lock for resourceID held by holder.
// Returns a token on success, or an error if the lock is held by another process.
func (s *LockStore) Acquire(ctx context.Context, resourceID, holder string) (string, error) {
	token, err := genToken()
	if err != nil {
		return "", fmt.Errorf("generating lock token: %w", err)
	}

	item := map[string]attrValue{
		"LockID": {S: resourceID},
		"Holder": {S: holder},
		"Token":  {S: token},
	}

	_, err = s.client.putItem(ctx, &putInput{
		TableName:  s.table,
		Item:       item,
		ConditionExpression: "attribute_not_exists(LockID)",
		ExpressionAttributeValues: map[string]attrValue{
			"#id": {S: resourceID},
		},
	})
	if err != nil {
		return "", fmt.Errorf("acquiring lock %s: %w", resourceID, err)
	}

	return token, nil
}

// Release releases a previously acquired lock. Only the holder with the
// matching token can release.
func (s *LockStore) Release(ctx context.Context, resourceID, token string) error {
	key := map[string]attrValue{
		"LockID": {S: resourceID},
	}

	_, err := s.client.deleteItem(ctx, &deleteInput{
		TableName:             s.table,
		Key:                   key,
		ConditionExpression:  "Token = :token",
		ExpressionAttributeValues: map[string]attrValue{
			":token": {S: token},
		},
	})
	if err != nil {
		return fmt.Errorf("releasing lock %s: %w", resourceID, err)
	}
	return nil
}

func genToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// localStatePath returns the path to the local state cache file.
func localStatePath() string {
	return ".fabrica/state.json"
}
