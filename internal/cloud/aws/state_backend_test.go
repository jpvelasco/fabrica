package aws

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
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

func newBootstrapTestProvider(s3Client *fakeS3StateBackendClient, ddbClient *fakeDynamoDBStateBackendClient, tableWaiter *fakeTableExistsWaiter, cfg *config.Config) *awsProvider {
	return &awsProvider{
		cfg: cfg,
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
		newTableExistsWaiter: func(client dynamodb.DescribeTableAPIClient) stateBackendTableExistsWaiter {
			return tableWaiter
		},
	}
}

func TestEnsureStateBucketCreatesWithConfig(t *testing.T) {
	s3Client := &fakeS3StateBackendClient{headErr: apiErr("404")}
	p := newBootstrapTestProvider(s3Client, nil, nil, nil)

	res, err := p.EnsureStateBucket(context.Background(), "fabrica-state-123", "us-west-2")
	if err != nil {
		t.Fatalf("EnsureStateBucket: %v", err)
	}
	if !res.Created {
		t.Error("Created = false, want true")
	}
	if !s3Client.createCalled || !s3Client.versioningCalled || !s3Client.encryptionCalled || !s3Client.pabCalled {
		t.Errorf("expected all configure calls: %+v", s3Client)
	}
	if s3Client.locationConstraint != "us-west-2" {
		t.Errorf("locationConstraint = %q, want us-west-2", s3Client.locationConstraint)
	}
	if s3Client.sseAlgorithm != "AES256" {
		t.Errorf("sseAlgorithm = %q, want AES256", s3Client.sseAlgorithm)
	}
}

func TestEnsureStateBucketUsEast1OmitsConstraint(t *testing.T) {
	s3Client := &fakeS3StateBackendClient{headErr: apiErr("404")}
	p := newBootstrapTestProvider(s3Client, nil, nil, nil)

	if _, err := p.EnsureStateBucket(context.Background(), "b", "us-east-1"); err != nil {
		t.Fatalf("EnsureStateBucket: %v", err)
	}
	if s3Client.locationConstraint != "" {
		t.Errorf("locationConstraint = %q, want empty for us-east-1", s3Client.locationConstraint)
	}
}

func TestEnsureStateBucketIdempotentReconciles(t *testing.T) {
	s3Client := &fakeS3StateBackendClient{} // HeadBucket succeeds => exists
	p := newBootstrapTestProvider(s3Client, nil, nil, nil)

	res, err := p.EnsureStateBucket(context.Background(), "b", "us-west-2")
	if err != nil {
		t.Fatalf("EnsureStateBucket: %v", err)
	}
	if res.Created {
		t.Error("Created = true, want false for existing bucket")
	}
	if s3Client.createCalled {
		t.Error("CreateBucket should not be called for existing bucket")
	}
	if !s3Client.versioningCalled || !s3Client.encryptionCalled || !s3Client.pabCalled {
		t.Error("config should be reconciled on existing bucket")
	}
}

func TestEnsureStateBucketKMSEncryption(t *testing.T) {
	cfg := config.Defaults()
	cfg.State.KMSKeyID = "alias/fabrica"
	s3Client := &fakeS3StateBackendClient{headErr: apiErr("404")}
	p := newBootstrapTestProvider(s3Client, nil, nil, cfg)

	if _, err := p.EnsureStateBucket(context.Background(), "b", "us-west-2"); err != nil {
		t.Fatalf("EnsureStateBucket: %v", err)
	}
	if s3Client.sseAlgorithm != "aws:kms" {
		t.Errorf("sseAlgorithm = %q, want aws:kms", s3Client.sseAlgorithm)
	}
	if s3Client.kmsKeyID != "alias/fabrica" {
		t.Errorf("kmsKeyID = %q, want alias/fabrica", s3Client.kmsKeyID)
	}
}

func TestEnsureStateBucketCreateErrorPropagates(t *testing.T) {
	s3Client := &fakeS3StateBackendClient{headErr: apiErr("404"), createErr: fmt.Errorf("boom")}
	p := newBootstrapTestProvider(s3Client, nil, nil, nil)

	if _, err := p.EnsureStateBucket(context.Background(), "b", "us-west-2"); err == nil {
		t.Fatal("expected error")
	}
	if s3Client.versioningCalled {
		t.Error("versioning should not run after create failure")
	}
}

func TestEnsureStateBucketPublicAccessBlockErrorPropagates(t *testing.T) {
	s3Client := &fakeS3StateBackendClient{headErr: apiErr("404"), pabErr: fmt.Errorf("denied")}
	p := newBootstrapTestProvider(s3Client, nil, nil, nil)

	if _, err := p.EnsureStateBucket(context.Background(), "b", "us-west-2"); err == nil {
		t.Fatal("expected error")
	}
}

func TestEnsureStateLockTableCreatesAndWaits(t *testing.T) {
	ddbClient := &fakeDynamoDBStateBackendClient{describeErr: apiErr("ResourceNotFoundException")}
	waiter := &fakeTableExistsWaiter{}
	p := newBootstrapTestProvider(nil, ddbClient, waiter, nil)

	res, err := p.EnsureStateLockTable(context.Background(), "fabrica-state-lock")
	if err != nil {
		t.Fatalf("EnsureStateLockTable: %v", err)
	}
	if !res.Created || !ddbClient.createCalled || waiter.calls != 1 {
		t.Errorf("expected create+wait: created=%v createCalled=%v waits=%d", res.Created, ddbClient.createCalled, waiter.calls)
	}
	if ddbClient.createKey != "LockID" {
		t.Errorf("hash key = %q, want LockID", ddbClient.createKey)
	}
}

func TestEnsureStateLockTableIdempotent(t *testing.T) {
	ddbClient := &fakeDynamoDBStateBackendClient{} // DescribeTable succeeds => exists
	waiter := &fakeTableExistsWaiter{}
	p := newBootstrapTestProvider(nil, ddbClient, waiter, nil)

	res, err := p.EnsureStateLockTable(context.Background(), "t")
	if err != nil {
		t.Fatalf("EnsureStateLockTable: %v", err)
	}
	if res.Created || ddbClient.createCalled || waiter.calls != 0 {
		t.Errorf("should be no-op for existing table: %+v", ddbClient)
	}
}

func TestEnsureStateLockTableCreateErrorPropagates(t *testing.T) {
	ddbClient := &fakeDynamoDBStateBackendClient{describeErr: apiErr("ResourceNotFoundException"), createErr: fmt.Errorf("denied")}
	waiter := &fakeTableExistsWaiter{}
	p := newBootstrapTestProvider(nil, ddbClient, waiter, nil)

	if _, err := p.EnsureStateLockTable(context.Background(), "t"); err == nil {
		t.Fatal("expected error")
	}
	if waiter.calls != 0 {
		t.Error("waiter should not run after create failure")
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
	locationConstraint string
	kmsKeyID           string
	sseAlgorithm       string
	createErr          error
	versioningErr      error
	encryptionErr      error
	pabErr             error
}

func (f *fakeS3StateBackendClient) HeadBucket(ctx context.Context, in *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	f.headCalls++
	f.headBucket = awssdk.ToString(in.Bucket)
	return &s3.HeadBucketOutput{}, f.headErr
}

func (f *fakeS3StateBackendClient) DeleteBucket(ctx context.Context, in *s3.DeleteBucketInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketOutput, error) {
	f.deleteCalls++
	f.deleteBucket = awssdk.ToString(in.Bucket)
	return &s3.DeleteBucketOutput{}, f.deleteErr
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

type fakeDynamoDBStateBackendClient struct {
	describeCalls int
	deleteCalls   int
	describeTable string
	deleteTable   string
	describeErr   error
	deleteErr     error

	createCalled bool
	createKey    string
	createErr    error
}

func (f *fakeDynamoDBStateBackendClient) DescribeTable(ctx context.Context, in *dynamodb.DescribeTableInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	f.describeCalls++
	f.describeTable = awssdk.ToString(in.TableName)
	return &dynamodb.DescribeTableOutput{}, f.describeErr
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
	return &dynamodb.CreateTableOutput{}, f.createErr
}

type fakeTableExistsWaiter struct {
	calls int
	table string
	err   error
}

func (f *fakeTableExistsWaiter) Wait(ctx context.Context, in *dynamodb.DescribeTableInput, maxWait time.Duration, optFns ...func(*dynamodb.TableExistsWaiterOptions)) error {
	f.calls++
	f.table = awssdk.ToString(in.TableName)
	return f.err
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
