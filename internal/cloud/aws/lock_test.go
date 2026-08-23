package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

type fakeLockDynamo struct {
	putErr      error
	deleteErr   error
	putCalls    int
	deleteCalls int
	lastPut     *dynamodb.PutItemInput
	lastDelete  *dynamodb.DeleteItemInput
}

func (f *fakeLockDynamo) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.putCalls++
	f.lastPut = in
	return nil, f.putErr
}

func (f *fakeLockDynamo) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.deleteCalls++
	f.lastDelete = in
	return nil, f.deleteErr
}

func newLockAdapterTest(t *testing.T, fake *fakeLockDynamo) *awsProvider {
	t.Helper()
	return &awsProvider{
		loadConfig: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{Region: "us-east-1"}, nil
		},
		newLockDynamoClient: func(awssdk.Config) lockDynamoClient { return fake },
	}
}

var _ = dynamodbtypes.AttributeValueMemberS{Value: ""} // keep types import stable

func TestAcquireStateLockRowSuccess(t *testing.T) {
	fake := &fakeLockDynamo{}
	p := newLockAdapterTest(t, fake)

	item := map[string]string{"LockID": "fabrica-state/123", "Holder": "op", "Token": "tok", "AcquiredAt": "1800000000"}
	err := p.AcquireStateLockRow(context.Background(), "fabrica-state-lock", item,
		"attribute_not_exists(LockID) OR AcquiredAt <= :stale",
		map[string]string{":stale": "1799999400"})
	if err != nil {
		t.Fatalf("AcquireStateLockRow: %v", err)
	}
	if fake.lastPut == nil || awssdk.ToString(fake.lastPut.TableName) != "fabrica-state-lock" {
		t.Fatalf("unexpected put: %+v", fake.lastPut)
	}
	if fake.lastPut.ConditionExpression == nil {
		t.Fatal("condition expression missing")
	}
}

func TestAcquireStateLockRowConditionalFailureMapsToErrLockHeld(t *testing.T) {
	fake := &fakeLockDynamo{
		putErr: &dynamodbtypes.ConditionalCheckFailedException{},
	}
	p := newLockAdapterTest(t, fake)

	err := p.AcquireStateLockRow(context.Background(), "t", map[string]string{"LockID": "x"}, "cond", nil)
	if !errors.Is(err, fabricac.ErrLockHeld) {
		t.Fatalf("err = %v, want ErrLockHeld", err)
	}
}

func TestAcquireStateLockRowOtherErrorWrapped(t *testing.T) {
	fake := &fakeLockDynamo{putErr: errors.New("throttled")}
	p := newLockAdapterTest(t, fake)

	err := p.AcquireStateLockRow(context.Background(), "t", map[string]string{"LockID": "x"}, "cond", nil)
	if err == nil || errors.Is(err, fabricac.ErrLockHeld) {
		t.Fatalf("err = %v, want wrapped non-held error", err)
	}
	if !strings.Contains(err.Error(), "putting state lock row") {
		t.Errorf("err = %v, want table context", err)
	}
}

func TestReleaseStateLockRowConditionalFailureMapsToErrLockHeld(t *testing.T) {
	fake := &fakeLockDynamo{
		deleteErr: &dynamodbtypes.ConditionalCheckFailedException{},
	}
	p := newLockAdapterTest(t, fake)

	err := p.ReleaseStateLockRow(context.Background(), "t", "fabrica-state/123", "tok")
	if !errors.Is(err, fabricac.ErrLockHeld) {
		t.Fatalf("err = %v, want ErrLockHeld", err)
	}
	if fake.lastDelete.Key["LockID"].(*dynamodbtypes.AttributeValueMemberS).Value != "fabrica-state/123" {
		t.Errorf("delete key = %+v", fake.lastDelete.Key)
	}
}

func TestAcquireStateLockRowConfigError(t *testing.T) {
	p := &awsProvider{
		loadConfig: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{}, context.Canceled
		},
	}
	err := p.AcquireStateLockRow(context.Background(), "t", map[string]string{"LockID": "x"}, "cond", nil)
	if err == nil || strings.Contains(fmt.Sprintf("%T", err), "ErrLockHeld") {
		t.Fatalf("err = %v, want config-load failure", err)
	}
}

func TestReleaseStateLockRowConfigError(t *testing.T) {
	p := &awsProvider{
		loadConfig: func(_ context.Context, _, _ string) (awssdk.Config, error) {
			return awssdk.Config{}, context.Canceled
		},
	}
	err := p.ReleaseStateLockRow(context.Background(), "t", "fabrica-state/1", "tok")
	if err == nil || errors.Is(err, fabricac.ErrLockHeld) {
		t.Fatalf("err = %v, want config-load failure", err)
	}
}

func TestLockDynamoDefaultFactory(t *testing.T) {
	p := &awsProvider{}
	if got := p.lockDynamo(awssdk.Config{Region: "us-east-1"}); got == nil {
		t.Fatal("default factory returned nil client")
	}
}
