package aws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

func TestStateBucketExistsUsesS3HeadBucket(t *testing.T) {
	tests := []struct {
		name      string
		headErr   error
		wantOK    bool
		wantErr   bool
		errSubstr string
	}{
		{
			name:   "exists",
			wantOK: true,
		},
		{
			name:    "missing by status code",
			headErr: apiErr("404"),
		},
		{
			name:    "missing by service code",
			headErr: apiErr("NoSuchBucket"),
		},
		{
			name:    "missing by NotFound code",
			headErr: apiErr("NotFound"),
		},
		{
			name:      "unexpected error",
			headErr:   apiErr("AccessDenied"),
			wantErr:   true,
			errSubstr: "checking S3 bucket state-bucket",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s3Client := &fakeS3StateBackendClient{headErr: tt.headErr}
			p := newStateBackendTestProvider(s3Client, nil, nil, nil)

			got, err := p.StateBucketExists(context.Background(), "state-bucket")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				assertStringContains(t, err.Error(), tt.errSubstr)
			} else if err != nil {
				t.Fatalf("StateBucketExists: %v", err)
			}
			if got != tt.wantOK {
				t.Fatalf("exists = %v, want %v", got, tt.wantOK)
			}
			if s3Client.headCalls != 1 {
				t.Fatalf("HeadBucket calls = %d, want 1", s3Client.headCalls)
			}
			if s3Client.headBucket != "state-bucket" {
				t.Fatalf("HeadBucket bucket = %q, want state-bucket", s3Client.headBucket)
			}
		})
	}
}

func TestStateLockTableExistsUsesDynamoDBDescribeTable(t *testing.T) {
	tests := []struct {
		name      string
		tableErr  error
		wantOK    bool
		wantErr   bool
		errSubstr string
	}{
		{
			name:   "exists",
			wantOK: true,
		},
		{
			name:     "missing",
			tableErr: apiErr("ResourceNotFoundException"),
		},
		{
			name:      "unexpected error",
			tableErr:  apiErr("AccessDenied"),
			wantErr:   true,
			errSubstr: "checking DynamoDB table state-locks",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ddbClient := &fakeDynamoDBStateBackendClient{describeErr: tt.tableErr}
			p := newStateBackendTestProvider(nil, ddbClient, nil, nil)

			got, err := p.StateLockTableExists(context.Background(), "state-locks")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				assertStringContains(t, err.Error(), tt.errSubstr)
			} else if err != nil {
				t.Fatalf("StateLockTableExists: %v", err)
			}
			if got != tt.wantOK {
				t.Fatalf("exists = %v, want %v", got, tt.wantOK)
			}
			if ddbClient.describeCalls != 1 {
				t.Fatalf("DescribeTable calls = %d, want 1", ddbClient.describeCalls)
			}
			if ddbClient.describeTable != "state-locks" {
				t.Fatalf("DescribeTable table = %q, want state-locks", ddbClient.describeTable)
			}
		})
	}
}

func TestDeleteStateBucketUsesS3DeleteAndWaiter(t *testing.T) {
	tests := []struct {
		name        string
		deleteErr   error
		waitErr     error
		wantDeleted bool
		wantMissing bool
		wantErr     bool
		wantIs      error
		errSubstr   string
		wantWait    bool
	}{
		{
			name:        "happy path",
			wantDeleted: true,
			wantWait:    true,
		},
		{
			name:        "already missing",
			deleteErr:   apiErr("NoSuchBucket"),
			wantMissing: true,
		},
		{
			name:        "missing by status code",
			deleteErr:   apiErr("404"),
			wantMissing: true,
		},
		{
			name:      "bucket not empty",
			deleteErr: apiErr("BucketNotEmpty"),
			wantErr:   true,
			wantIs:    fabricac.ErrStateBucketNotEmpty,
			errSubstr: "deleting S3 bucket state-bucket",
		},
		{
			name:      "unexpected delete error",
			deleteErr: apiErr("AccessDenied"),
			wantErr:   true,
			errSubstr: "deleting S3 bucket state-bucket",
		},
		{
			name:      "waiter failure",
			waitErr:   fmt.Errorf("wait timed out"),
			wantErr:   true,
			errSubstr: "waiting for S3 bucket state-bucket deletion",
			wantWait:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s3Client := &fakeS3StateBackendClient{deleteErr: tt.deleteErr}
			waiter := &fakeBucketNotExistsWaiter{err: tt.waitErr}
			p := newStateBackendTestProvider(s3Client, nil, waiter, nil)

			result, err := p.DeleteStateBucket(context.Background(), "state-bucket")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				assertStringContains(t, err.Error(), tt.errSubstr)
				if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
					t.Fatalf("error %v does not wrap %v", err, tt.wantIs)
				}
			} else if err != nil {
				t.Fatalf("DeleteStateBucket: %v", err)
			}
			if result.Identifier != "state-bucket" {
				t.Fatalf("Identifier = %q, want state-bucket", result.Identifier)
			}
			if result.Deleted != tt.wantDeleted {
				t.Fatalf("Deleted = %v, want %v", result.Deleted, tt.wantDeleted)
			}
			if result.Missing != tt.wantMissing {
				t.Fatalf("Missing = %v, want %v", result.Missing, tt.wantMissing)
			}
			if s3Client.deleteCalls != 1 {
				t.Fatalf("DeleteBucket calls = %d, want 1", s3Client.deleteCalls)
			}
			if s3Client.deleteBucket != "state-bucket" {
				t.Fatalf("DeleteBucket bucket = %q, want state-bucket", s3Client.deleteBucket)
			}
			assertWaiterUse(t, waiter.calls, tt.wantWait, "bucket waiter")
			if tt.wantWait {
				if waiter.bucket != "state-bucket" {
					t.Fatalf("waiter bucket = %q, want state-bucket", waiter.bucket)
				}
				if waiter.maxWait != 2*time.Minute {
					t.Fatalf("waiter max wait = %s, want 2m0s", waiter.maxWait)
				}
			}
		})
	}
}

func TestDeleteStateBucketReturnsConfigErrorWithoutCallingS3(t *testing.T) {
	s3Client := &fakeS3StateBackendClient{}
	p := newStateBackendTestProvider(s3Client, nil, nil, nil)
	p.loadConfig = func(context.Context, string, string) (awssdk.Config, error) {
		return awssdk.Config{}, fmt.Errorf("config unavailable")
	}

	_, err := p.DeleteStateBucket(context.Background(), "state-bucket")

	if err == nil {
		t.Fatal("expected error")
	}
	assertStringContains(t, err.Error(), "config unavailable")
	if s3Client.deleteCalls != 0 {
		t.Fatalf("DeleteBucket calls = %d, want 0", s3Client.deleteCalls)
	}
}

func TestDeleteStateLockTableUsesDynamoDBDeleteAndWaiter(t *testing.T) {
	tests := []struct {
		name        string
		deleteErr   error
		waitErr     error
		wantDeleted bool
		wantMissing bool
		wantErr     bool
		errSubstr   string
		wantWait    bool
	}{
		{
			name:        "happy path",
			wantDeleted: true,
			wantWait:    true,
		},
		{
			name:        "already missing",
			deleteErr:   apiErr("ResourceNotFoundException"),
			wantMissing: true,
		},
		{
			name:      "unexpected delete error",
			deleteErr: apiErr("AccessDenied"),
			wantErr:   true,
			errSubstr: "deleting DynamoDB table state-locks",
		},
		{
			name:      "waiter failure",
			waitErr:   fmt.Errorf("wait timed out"),
			wantErr:   true,
			errSubstr: "waiting for DynamoDB table state-locks deletion",
			wantWait:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ddbClient := &fakeDynamoDBStateBackendClient{deleteErr: tt.deleteErr}
			waiter := &fakeTableNotExistsWaiter{err: tt.waitErr}
			p := newStateBackendTestProvider(nil, ddbClient, nil, waiter)

			result, err := p.DeleteStateLockTable(context.Background(), "state-locks")

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				assertStringContains(t, err.Error(), tt.errSubstr)
			} else if err != nil {
				t.Fatalf("DeleteStateLockTable: %v", err)
			}
			if result.Identifier != "state-locks" {
				t.Fatalf("Identifier = %q, want state-locks", result.Identifier)
			}
			if result.Deleted != tt.wantDeleted {
				t.Fatalf("Deleted = %v, want %v", result.Deleted, tt.wantDeleted)
			}
			if result.Missing != tt.wantMissing {
				t.Fatalf("Missing = %v, want %v", result.Missing, tt.wantMissing)
			}
			if ddbClient.deleteCalls != 1 {
				t.Fatalf("DeleteTable calls = %d, want 1", ddbClient.deleteCalls)
			}
			if ddbClient.deleteTable != "state-locks" {
				t.Fatalf("DeleteTable table = %q, want state-locks", ddbClient.deleteTable)
			}
			assertWaiterUse(t, waiter.calls, tt.wantWait, "table waiter")
			if tt.wantWait {
				if waiter.table != "state-locks" {
					t.Fatalf("waiter table = %q, want state-locks", waiter.table)
				}
				if waiter.maxWait != 2*time.Minute {
					t.Fatalf("waiter max wait = %s, want 2m0s", waiter.maxWait)
				}
			}
		})
	}
}

func TestStateBackendConfigLoaderReceivesRegionAndProfile(t *testing.T) {
	s3Client := &fakeS3StateBackendClient{}
	var gotRegion string
	var gotProfile string
	p := newStateBackendTestProvider(s3Client, nil, &fakeBucketNotExistsWaiter{}, nil)
	p.awsCfg.region = "us-west-2"
	p.awsCfg.profile = "studio-admin"
	p.loadConfig = func(ctx context.Context, region, profile string) (awssdk.Config, error) {
		gotRegion = region
		gotProfile = profile
		return awssdk.Config{Region: region}, nil
	}

	if _, err := p.DeleteStateBucket(context.Background(), "state-bucket"); err != nil {
		t.Fatalf("DeleteStateBucket: %v", err)
	}
	if gotRegion != "us-west-2" {
		t.Fatalf("region = %q, want us-west-2", gotRegion)
	}
	if gotProfile != "studio-admin" {
		t.Fatalf("profile = %q, want studio-admin", gotProfile)
	}
}

func newStateBackendTestProvider(s3Client *fakeS3StateBackendClient, ddbClient *fakeDynamoDBStateBackendClient, bucketWaiter *fakeBucketNotExistsWaiter, tableWaiter *fakeTableNotExistsWaiter) *awsProvider {
	return &awsProvider{
		awsCfg: awsConfig{
			region:  "us-east-1",
			profile: "unit-test",
		},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{Region: region}, nil
		},
		newS3StateClient: func(cfg awssdk.Config) stateBackendS3Client {
			return s3Client
		},
		newDynamoDBStateClient: func(cfg awssdk.Config) stateBackendDynamoDBClient {
			return ddbClient
		},
		newBucketNotExistsWaiter: func(client s3.HeadBucketAPIClient) stateBackendBucketWaiter {
			return bucketWaiter
		},
		newTableNotExistsWaiter: func(client dynamodb.DescribeTableAPIClient) stateBackendTableWaiter {
			return tableWaiter
		},
	}
}

func TestS3StateClientDefaultConstructor(t *testing.T) {
	p := &awsProvider{
		awsCfg: awsConfig{region: "us-east-1"},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{Region: region}, nil
		},
	}
	cfg := awssdk.Config{Region: "us-east-1"}
	client := p.s3StateClient(cfg)
	if client == nil {
		t.Fatal("s3StateClient returned nil with nil seam")
	}
}

func TestDynamoDBStateClientDefaultConstructor(t *testing.T) {
	p := &awsProvider{
		awsCfg: awsConfig{region: "us-east-1"},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{Region: region}, nil
		},
	}
	cfg := awssdk.Config{Region: "us-east-1"}
	client := p.dynamoDBStateClient(cfg)
	if client == nil {
		t.Fatal("dynamoDBStateClient returned nil with nil seam")
	}
}

func TestWaiterDefaultConstructors(t *testing.T) {
	p := &awsProvider{
		awsCfg: awsConfig{region: "us-east-1"},
		loadConfig: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{Region: region}, nil
		},
	}
	s3Client := s3.NewFromConfig(awssdk.Config{Region: "us-east-1"})
	ddbClient := dynamodb.NewFromConfig(awssdk.Config{Region: "us-east-1"})

	bucketWaiter := p.bucketNotExistsWaiter(s3Client)
	if bucketWaiter == nil {
		t.Fatal("bucketNotExistsWaiter returned nil with nil seam")
	}

	tableWaiter := p.tableNotExistsWaiter(ddbClient)
	if tableWaiter == nil {
		t.Fatal("tableNotExistsWaiter returned nil with nil seam")
	}

	tableExistsWaiter := p.tableExistsWaiter(ddbClient)
	if tableExistsWaiter == nil {
		t.Fatal("tableExistsWaiter returned nil with nil seam")
	}
}

type fakeS3StateBackendClient struct {
	headCalls    int
	deleteCalls  int
	headBucket   string
	deleteBucket string
	headErr      error
	deleteErr    error

	createCalled       bool
	versioningCalled   bool
	encryptionCalled   bool
	pabCalled          bool
	taggingCalled      bool
	taggedBucket       string
	bucketTags         map[string]string
	locationConstraint string
	kmsKeyID           string
	sseAlgorithm       string
	createErr          error
	versioningErr      error
	encryptionErr      error
	pabErr             error
	taggingErr         error

	objectBody    string
	getObjectKey  string
	putObjectKey  string
	putObjectBody string
	getObjectErr  error
	putObjectErr  error
}

func (f *fakeS3StateBackendClient) HeadBucket(ctx context.Context, in *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	f.headCalls++
	f.headBucket = awssdk.ToString(in.Bucket)
	return &s3.HeadBucketOutput{}, f.headErr
}

func (f *fakeS3StateBackendClient) GetBucketTagging(context.Context, *s3.GetBucketTaggingInput, ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error) {
	return &s3.GetBucketTaggingOutput{}, nil
}

func (f *fakeS3StateBackendClient) DeleteBucket(ctx context.Context, in *s3.DeleteBucketInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketOutput, error) {
	f.deleteCalls++
	f.deleteBucket = awssdk.ToString(in.Bucket)
	return &s3.DeleteBucketOutput{}, f.deleteErr
}

func (f *fakeS3StateBackendClient) GetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.getObjectKey = awssdk.ToString(in.Key)
	if f.getObjectErr != nil {
		return nil, f.getObjectErr
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader([]byte(f.objectBody)))}, nil
}

func (f *fakeS3StateBackendClient) PutObject(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.putObjectKey = awssdk.ToString(in.Key)
	if f.putObjectErr != nil {
		return nil, f.putObjectErr
	}
	if in.Body != nil {
		data, _ := io.ReadAll(in.Body)
		f.putObjectBody = string(data)
	}
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3StateBackendClient) CreateBucket(ctx context.Context, in *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	f.createCalled = true
	if in.CreateBucketConfiguration != nil {
		f.locationConstraint = string(in.CreateBucketConfiguration.LocationConstraint)
	}
	return &s3.CreateBucketOutput{}, f.createErr
}

func (f *fakeS3StateBackendClient) PutBucketVersioning(ctx context.Context, in *s3.PutBucketVersioningInput, optFns ...func(*s3.Options)) (*s3.PutBucketVersioningOutput, error) {
	f.versioningCalled = true
	return &s3.PutBucketVersioningOutput{}, f.versioningErr
}

func (f *fakeS3StateBackendClient) PutBucketEncryption(ctx context.Context, in *s3.PutBucketEncryptionInput, optFns ...func(*s3.Options)) (*s3.PutBucketEncryptionOutput, error) {
	f.encryptionCalled = true
	if in.ServerSideEncryptionConfiguration != nil && len(in.ServerSideEncryptionConfiguration.Rules) > 0 {
		def := in.ServerSideEncryptionConfiguration.Rules[0].ApplyServerSideEncryptionByDefault
		if def != nil {
			f.sseAlgorithm = string(def.SSEAlgorithm)
			f.kmsKeyID = awssdk.ToString(def.KMSMasterKeyID)
		}
	}
	return &s3.PutBucketEncryptionOutput{}, f.encryptionErr
}

func (f *fakeS3StateBackendClient) PutPublicAccessBlock(ctx context.Context, in *s3.PutPublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error) {
	f.pabCalled = true
	return &s3.PutPublicAccessBlockOutput{}, f.pabErr
}

func (f *fakeS3StateBackendClient) PutBucketTagging(ctx context.Context, in *s3.PutBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.PutBucketTaggingOutput, error) {
	f.taggingCalled = true
	f.taggedBucket = awssdk.ToString(in.Bucket)
	f.bucketTags = map[string]string{}
	for _, tag := range in.Tagging.TagSet {
		f.bucketTags[awssdk.ToString(tag.Key)] = awssdk.ToString(tag.Value)
	}
	return &s3.PutBucketTaggingOutput{}, f.taggingErr
}

type fakeDynamoDBStateBackendClient struct {
	describeCalls int
	deleteCalls   int
	describeTable string
	deleteTable   string
	describeErr   error
	deleteErr     error

	createCalled bool
	createKey    string
	createTags   []dynamodbtypes.Tag
	createErr    error
}

func (f *fakeDynamoDBStateBackendClient) DescribeTable(ctx context.Context, in *dynamodb.DescribeTableInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	f.describeCalls++
	f.describeTable = awssdk.ToString(in.TableName)
	return &dynamodb.DescribeTableOutput{}, f.describeErr
}

func (f *fakeDynamoDBStateBackendClient) ListTagsOfResource(context.Context, *dynamodb.ListTagsOfResourceInput, ...func(*dynamodb.Options)) (*dynamodb.ListTagsOfResourceOutput, error) {
	return &dynamodb.ListTagsOfResourceOutput{}, nil
}

func (f *fakeDynamoDBStateBackendClient) TagResource(context.Context, *dynamodb.TagResourceInput, ...func(*dynamodb.Options)) (*dynamodb.TagResourceOutput, error) {
	return &dynamodb.TagResourceOutput{}, nil
}

func (f *fakeDynamoDBStateBackendClient) DeleteTable(ctx context.Context, in *dynamodb.DeleteTableInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteTableOutput, error) {
	f.deleteCalls++
	f.deleteTable = awssdk.ToString(in.TableName)
	return &dynamodb.DeleteTableOutput{}, f.deleteErr
}

func (f *fakeDynamoDBStateBackendClient) CreateTable(ctx context.Context, in *dynamodb.CreateTableInput, optFns ...func(*dynamodb.Options)) (*dynamodb.CreateTableOutput, error) {
	f.createCalled = true
	if len(in.KeySchema) > 0 {
		f.createKey = awssdk.ToString(in.KeySchema[0].AttributeName)
	}
	f.createTags = in.Tags
	return &dynamodb.CreateTableOutput{}, f.createErr
}

type fakeBucketNotExistsWaiter struct {
	calls   int
	bucket  string
	maxWait time.Duration
	err     error
}

func (f *fakeBucketNotExistsWaiter) Wait(ctx context.Context, in *s3.HeadBucketInput, maxWait time.Duration, optFns ...func(*s3.BucketNotExistsWaiterOptions)) error {
	f.calls++
	f.bucket = awssdk.ToString(in.Bucket)
	f.maxWait = maxWait
	return f.err
}

type fakeTableNotExistsWaiter struct {
	calls   int
	table   string
	maxWait time.Duration
	err     error
}

func (f *fakeTableNotExistsWaiter) Wait(ctx context.Context, in *dynamodb.DescribeTableInput, maxWait time.Duration, optFns ...func(*dynamodb.TableNotExistsWaiterOptions)) error {
	f.calls++
	f.table = awssdk.ToString(in.TableName)
	f.maxWait = maxWait
	return f.err
}

func apiErr(code string) error {
	return &smithy.GenericAPIError{Code: code, Message: code}
}

func assertWaiterUse(t *testing.T, calls int, wantUsed bool, name string) {
	t.Helper()
	if wantUsed && calls != 1 {
		t.Fatalf("%s calls = %d, want 1", name, calls)
	}
	if !wantUsed && calls != 0 {
		t.Fatalf("%s calls = %d, want 0", name, calls)
	}
}

func assertStringContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}

// TestStateBackendDeleteTimeoutCoversWaiterBounds pins the invariant that the
// parent delete context outlives the declared 2-minute waiter maximums;
// otherwise the waiter can never use its stated budget.
func TestStateBackendDeleteTimeoutCoversWaiterBounds(t *testing.T) {
	const waiterMax = 2 * time.Minute
	if stateBackendDeleteTimeout <= waiterMax {
		t.Fatalf("stateBackendDeleteTimeout = %v must exceed the %v waiter bound", stateBackendDeleteTimeout, waiterMax)
	}
}
