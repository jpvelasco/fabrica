# Milestone 1: Foundation & First-Run Experience — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire `fabrica setup` to really create the S3 state bucket + DynamoDB lock table, and add an aggregate read-only `fabrica status` overview command.

**Architecture:** Add a `StateBackendBootstrapper` capability interface in `internal/cloud` (mirroring the existing Checker/Destroyer pattern), implement it on `awsProvider` reusing existing seam factories, rewrite `state.Bootstrap()` to call it, and update `cmd/setup` with a y/N confirmation. Add a new read-only `cmd/status` package that reads local state, checks backend health, and reports per-module status with optional TCP probing.

**Tech Stack:** Go 1.25+, aws-sdk-go-v2 (s3, dynamodb), cobra, Viper (scoped to internal/config). `gofmt` only. `fmt.Print*` output only — no logging library.

## Global Constraints

- Go 1.25.11+; build/test with `go build ./...`, `go test ./...` (Windows: no `-race`).
- `internal/cloud/*` must NOT import `internal/state`, `internal/cost`, or any `cmd/*`.
- Error wrapping: `fmt.Errorf("context: %w", err)` — messages say what went wrong AND what to do. No sentinel errors except the existing documented ones.
- Acronyms fully uppercase: `ID`, `ARN`, `URL`, `AWS`, `IAM`. Files `snake_case.go`. `New*` returns pointers; single-letter receivers.
- Config structs live in `internal/config/config.go` with `mapstructure:` tags.
- Output: `fmt.Printf`/`Println` only. Coverage target ≥60% for touched `internal/*`.
- Two-package test pattern: white-box `*_test.go` (seam injection) + black-box `cobra_test.go` (minimal root replicating `--dry-run`/`--yes`/`--json` persistent flags).
- Conventional Commits: `feat|fix|refactor|test|docs|chore`.
- Lint: `golangci-lint run ./...` must pass (errcheck, govet, staticcheck, gosec, gocritic, dupl, etc.).

---

### Task 1: `StateBackendBootstrapper` interface + result type

**Files:**
- Modify: `internal/cloud/state_backend.go`
- Test: covered indirectly (interface only); compile-checked by Task 2's `var _` assertion.

**Interfaces:**
- Produces:
  - `type StateBackendCreateResult struct { Identifier string; Created bool }`
  - `type StateBackendBootstrapper interface { EnsureStateBucket(ctx context.Context, bucket, region string) (StateBackendCreateResult, error); EnsureStateLockTable(ctx context.Context, table string) (StateBackendCreateResult, error) }`

- [ ] **Step 1: Add the types to `internal/cloud/state_backend.go`**

Append after the existing `StateBackendDestroyer` block:

```go
// StateBackendCreateResult describes one idempotent state-backend creation.
type StateBackendCreateResult struct {
	Identifier string
	Created    bool // false => already existed (idempotent no-op)
}

// StateBackendBootstrapper creates the storage primitives used by Fabrica state.
type StateBackendBootstrapper interface {
	EnsureStateBucket(ctx context.Context, bucket, region string) (StateBackendCreateResult, error)
	EnsureStateLockTable(ctx context.Context, table string) (StateBackendCreateResult, error)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/cloud/...`
Expected: success, no output.

- [ ] **Step 3: Commit**

```bash
git add internal/cloud/state_backend.go
git commit -m "feat(cloud): add StateBackendBootstrapper interface"
```

---

### Task 2: AWS bootstrapper implementation

**Files:**
- Modify: `internal/cloud/aws/state_backend.go` (widen client interfaces, add waiter, add Ensure methods)
- Modify: `internal/cloud/aws/aws.go` (add `var _ fabricac.StateBackendBootstrapper` assertion)
- Test: `internal/cloud/aws/state_backend_test.go` (extend)

**Interfaces:**
- Consumes: `fabricac.StateBackendCreateResult`, `fabricac.StateBackendBootstrapper` (Task 1); existing `p.s3StateClient`, `p.dynamoDBStateClient`, `p.stateBackendConfig`.
- Produces: `(*awsProvider).EnsureStateBucket`, `(*awsProvider).EnsureStateLockTable`.

- [ ] **Step 1: Write failing tests in `internal/cloud/aws/state_backend_test.go`**

First inspect the existing fakes in this file (they already fake S3/DynamoDB for the checker/destroyer). Extend the fake S3 client to record `CreateBucket`/`PutBucketVersioning`/`PutBucketEncryption`/`PutPublicAccessBlock` calls and the fake DynamoDB client to record `CreateTable`. Add a fake table-exists waiter. Then add:

```go
func TestEnsureStateBucketCreatesWithConfig(t *testing.T) {
	fakeS3 := &fakeStateS3{headErr: notFound("404")} // bucket absent
	p := newTestProviderWithS3(fakeS3)               // helper mirroring existing test setup
	res, err := p.EnsureStateBucket(context.Background(), "fabrica-state-123", "us-west-2")
	if err != nil {
		t.Fatalf("EnsureStateBucket: %v", err)
	}
	if !res.Created {
		t.Errorf("Created = false, want true")
	}
	if !fakeS3.createCalled || !fakeS3.versioningCalled || !fakeS3.encryptionCalled || !fakeS3.pabCalled {
		t.Errorf("expected all configure calls: %+v", fakeS3)
	}
	if fakeS3.locationConstraint != "us-west-2" {
		t.Errorf("locationConstraint = %q, want us-west-2", fakeS3.locationConstraint)
	}
}

func TestEnsureStateBucketUsEast1OmitsConstraint(t *testing.T) {
	fakeS3 := &fakeStateS3{headErr: notFound("404")}
	p := newTestProviderWithS3(fakeS3)
	if _, err := p.EnsureStateBucket(context.Background(), "b", "us-east-1"); err != nil {
		t.Fatalf("EnsureStateBucket: %v", err)
	}
	if fakeS3.locationConstraint != "" {
		t.Errorf("locationConstraint = %q, want empty for us-east-1", fakeS3.locationConstraint)
	}
}

func TestEnsureStateBucketIdempotentReconciles(t *testing.T) {
	fakeS3 := &fakeStateS3{} // HeadBucket succeeds => exists
	p := newTestProviderWithS3(fakeS3)
	res, err := p.EnsureStateBucket(context.Background(), "b", "us-west-2")
	if err != nil {
		t.Fatalf("EnsureStateBucket: %v", err)
	}
	if res.Created {
		t.Errorf("Created = true, want false for existing bucket")
	}
	if fakeS3.createCalled {
		t.Errorf("CreateBucket should not be called for existing bucket")
	}
	if !fakeS3.versioningCalled || !fakeS3.encryptionCalled || !fakeS3.pabCalled {
		t.Errorf("config should be reconciled on existing bucket")
	}
}

func TestEnsureStateBucketKMSEncryption(t *testing.T) {
	fakeS3 := &fakeStateS3{headErr: notFound("404")}
	p := newTestProviderWithS3KMS(fakeS3, "alias/fabrica")
	if _, err := p.EnsureStateBucket(context.Background(), "b", "us-west-2"); err != nil {
		t.Fatalf("EnsureStateBucket: %v", err)
	}
	if fakeS3.kmsKeyID != "alias/fabrica" {
		t.Errorf("kmsKeyID = %q, want alias/fabrica", fakeS3.kmsKeyID)
	}
}

func TestEnsureStateLockTableCreatesAndWaits(t *testing.T) {
	fakeDB := &fakeStateDynamo{describeErr: notFound("ResourceNotFoundException")}
	p := newTestProviderWithDynamo(fakeDB)
	res, err := p.EnsureStateLockTable(context.Background(), "fabrica-state-lock")
	if err != nil {
		t.Fatalf("EnsureStateLockTable: %v", err)
	}
	if !res.Created || !fakeDB.createCalled || !fakeDB.waited {
		t.Errorf("expected create+wait: %+v", fakeDB)
	}
}

func TestEnsureStateLockTableIdempotent(t *testing.T) {
	fakeDB := &fakeStateDynamo{} // DescribeTable succeeds => exists
	p := newTestProviderWithDynamo(fakeDB)
	res, err := p.EnsureStateLockTable(context.Background(), "t")
	if err != nil {
		t.Fatalf("EnsureStateLockTable: %v", err)
	}
	if res.Created || fakeDB.createCalled {
		t.Errorf("should be no-op for existing table")
	}
}
```

NOTE: adapt fake/helper names to the existing test file's conventions; reuse the existing `notFound`/smithy-error helper if present, otherwise add one returning a `smithy.APIError` with the given code.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cloud/aws/ -run TestEnsureState -v`
Expected: FAIL (methods undefined / fields missing).

- [ ] **Step 3: Widen client interfaces and add waiter in `internal/cloud/aws/state_backend.go`**

Add to `stateBackendS3Client`:

```go
	CreateBucket(context.Context, *s3.CreateBucketInput, ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	PutBucketVersioning(context.Context, *s3.PutBucketVersioningInput, ...func(*s3.Options)) (*s3.PutBucketVersioningOutput, error)
	PutBucketEncryption(context.Context, *s3.PutBucketEncryptionInput, ...func(*s3.Options)) (*s3.PutBucketEncryptionOutput, error)
	PutPublicAccessBlock(context.Context, *s3.PutPublicAccessBlockInput, ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error)
```

Add to `stateBackendDynamoDBClient`:

```go
	CreateTable(context.Context, *dynamodb.CreateTableInput, ...func(*dynamodb.Options)) (*dynamodb.CreateTableOutput, error)
```

Add a table-exists waiter type + factory mirroring the existing not-exists ones:

```go
type stateBackendTableExistsWaiter interface {
	Wait(context.Context, *dynamodb.DescribeTableInput, time.Duration, ...func(*dynamodb.TableExistsWaiterOptions)) error
}
type stateBackendTableExistsWaiterFactory func(dynamodb.DescribeTableAPIClient) stateBackendTableExistsWaiter
```

Add the factory field to `awsProvider` (in `aws.go`): `newTableExistsWaiter stateBackendTableExistsWaiterFactory` and an accessor:

```go
func (p *awsProvider) tableExistsWaiter(client dynamodb.DescribeTableAPIClient) stateBackendTableExistsWaiter {
	if p.newTableExistsWaiter != nil {
		return p.newTableExistsWaiter(client)
	}
	return dynamodb.NewTableExistsWaiter(client)
}
```

- [ ] **Step 4: Implement `EnsureStateBucket` / `EnsureStateLockTable`**

```go
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
	if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "404" || apiErr.ErrorCode() == "NoSuchBucket" || apiErr.ErrorCode() == "NotFound") {
		return false, nil
	}
	return false, fmt.Errorf("checking S3 bucket %s: %w", bucket, err)
}

func (p *awsProvider) createBucket(ctx context.Context, client stateBackendS3Client, bucket, region string) error {
	in := &s3.CreateBucketInput{Bucket: aws.String(bucket)}
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
		return fmt.Errorf("enabling versioning on %s: %w", bucket, err)
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
		return fmt.Errorf("enabling encryption on %s: %w", bucket, err)
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
		return fmt.Errorf("blocking public access on %s: %w", bucket, err)
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
		return result, nil // exists
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
		return result, fmt.Errorf("creating DynamoDB table %s: %w — check dynamodb:CreateTable permission", table, err)
	}

	waiter := p.tableExistsWaiter(client)
	if err := waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(table)}, 2*time.Minute); err != nil {
		return result, fmt.Errorf("waiting for DynamoDB table %s to become active: %w", table, err)
	}
	result.Created = true
	return result, nil
}
```

Add imports: `s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"`, `dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"`.

- [ ] **Step 5: Add interface assertion in `internal/cloud/aws/aws.go`**

Next to the existing `var _ fabricac.Provider` block, add:

```go
var _ fabricac.StateBackendBootstrapper = (*awsProvider)(nil)
```

- [ ] **Step 6: Run tests + lint**

Run: `go test ./internal/cloud/aws/ -run TestEnsureState -v && go vet ./internal/cloud/aws/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/cloud/aws/
git commit -m "feat(aws): implement state backend bootstrapper (S3 + DynamoDB)"
```

---

### Task 3: Rewrite `state.Bootstrap()`

**Files:**
- Modify: `internal/state/bootstrap.go`
- Test: `internal/state/bootstrap_test.go` (new)

**Interfaces:**
- Consumes: `cloud.StateBackendBootstrapper`, `cloud.StateBackendCreateResult` (Task 1); `ResolveBackendNames` (existing).
- Produces: `Bootstrap(ctx, provider, cfg) ([]BootstrapResult, error)` — same signature, real behavior.

- [ ] **Step 1: Write failing tests in `internal/state/bootstrap_test.go`**

```go
package state

import (
	"context"
	"errors"
	"testing"

	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

type fakeBootProvider struct {
	account     string
	identityErr error
	bucketRes   fabricac.StateBackendCreateResult
	bucketErr   error
	tableRes    fabricac.StateBackendCreateResult
	tableErr    error
	tableCalled bool
}

func (f *fakeBootProvider) Name() string { return "fake" }
func (f *fakeBootProvider) Identity(ctx context.Context) (string, string, string, error) {
	return f.account, "arn", "us-west-2", f.identityErr
}
func (f *fakeBootProvider) Resources() fabricac.ResourceClient { return nil }
func (f *fakeBootProvider) EnsureStateBucket(ctx context.Context, bucket, region string) (fabricac.StateBackendCreateResult, error) {
	return f.bucketRes, f.bucketErr
}
func (f *fakeBootProvider) EnsureStateLockTable(ctx context.Context, table string) (fabricac.StateBackendCreateResult, error) {
	f.tableCalled = true
	return f.tableRes, f.tableErr
}

func cfgFor(account string) *config.Config {
	c := config.Defaults()
	c.Cloud.AWS.AccountID = account
	return c
}

func TestBootstrapCreatesBoth(t *testing.T) {
	p := &fakeBootProvider{
		account:   "123",
		bucketRes: fabricac.StateBackendCreateResult{Identifier: "fabrica-state-123", Created: true},
		tableRes:  fabricac.StateBackendCreateResult{Identifier: "fabrica-state-lock", Created: true},
	}
	results, err := Bootstrap(context.Background(), p, cfgFor("123"))
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Existed || results[1].Existed {
		t.Errorf("both should be newly created: %+v", results)
	}
}

func TestBootstrapIdempotent(t *testing.T) {
	p := &fakeBootProvider{
		account:   "123",
		bucketRes: fabricac.StateBackendCreateResult{Identifier: "b", Created: false},
		tableRes:  fabricac.StateBackendCreateResult{Identifier: "t", Created: false},
	}
	results, err := Bootstrap(context.Background(), p, cfgFor("123"))
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if !results[0].Existed || !results[1].Existed {
		t.Errorf("both should report Existed: %+v", results)
	}
}

func TestBootstrapBucketFailureSkipsTable(t *testing.T) {
	p := &fakeBootProvider{account: "123", bucketErr: errors.New("boom")}
	_, err := Bootstrap(context.Background(), p, cfgFor("123"))
	if err == nil {
		t.Fatal("expected error")
	}
	if p.tableCalled {
		t.Error("table creation should not be attempted after bucket failure")
	}
}

func TestBootstrapIdentityFailureFirst(t *testing.T) {
	p := &fakeBootProvider{identityErr: errors.New("no creds")}
	_, err := Bootstrap(context.Background(), p, cfgFor("123"))
	if err == nil {
		t.Fatal("expected identity error")
	}
}

type noBootProvider struct{ fakeBootProvider }

// noBootProvider deliberately does NOT implement StateBackendBootstrapper.
// Achieve this by NOT embedding the Ensure methods — declare a separate minimal type.
```

For the "provider lacks interface" case, declare a minimal provider type implementing only `cloud.Provider` (Name/Identity/Resources) and assert `Bootstrap` returns an error mentioning the provider name.

```go
type plainProvider struct{ account string }

func (plainProvider) Name() string { return "plain" }
func (p plainProvider) Identity(ctx context.Context) (string, string, string, error) {
	return p.account, "arn", "us-west-2", nil
}
func (plainProvider) Resources() fabricac.ResourceClient { return nil }

func TestBootstrapUnsupportedProvider(t *testing.T) {
	_, err := Bootstrap(context.Background(), plainProvider{account: "123"}, cfgFor("123"))
	if err == nil {
		t.Fatal("expected unsupported-provider error")
	}
}
```

Remove the `noBootProvider` stub above — `plainProvider` is the real test; keep the file clean.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/state/ -run TestBootstrap -v`
Expected: FAIL (still returns ErrBootstrapNotImplemented).

- [ ] **Step 3: Rewrite `internal/state/bootstrap.go`**

```go
package state

import (
	"context"
	"fmt"

	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

// BootstrapResult describes the outcome of one bootstrapping step.
type BootstrapResult struct {
	Name    string
	Existed bool
}

func (r BootstrapResult) String() string {
	if r.Existed {
		return fmt.Sprintf("  %s already exists — skipping", r.Name)
	}
	return fmt.Sprintf("  created %s", r.Name)
}

// Bootstrap creates the S3 state bucket and DynamoDB lock table for this account.
// The identity check runs first so credential problems surface immediately, then
// the bucket and table are created in order (bucket first). Each step is
// idempotent: an already-existing resource is reported with Existed=true.
func Bootstrap(ctx context.Context, provider fabricac.Provider, cfg *config.Config) ([]BootstrapResult, error) {
	account, _, region, err := provider.Identity(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving identity: %w", err)
	}

	bootstrapper, ok := provider.(fabricac.StateBackendBootstrapper)
	if !ok {
		return nil, fmt.Errorf("provider %s does not support state backend bootstrap — create the S3 bucket and DynamoDB table manually", provider.Name())
	}

	names := ResolveBackendNames(cfg, account)

	bucket, err := bootstrapper.EnsureStateBucket(ctx, names.Bucket, region)
	if err != nil {
		return nil, fmt.Errorf("creating state bucket %s: %w", names.Bucket, err)
	}

	table, err := bootstrapper.EnsureStateLockTable(ctx, names.Table)
	if err != nil {
		return nil, fmt.Errorf("creating lock table %s (state bucket %s was handled): %w", names.Table, names.Bucket, err)
	}

	return []BootstrapResult{
		{Name: "S3 bucket " + bucket.Identifier, Existed: !bucket.Created},
		{Name: "DynamoDB table " + table.Identifier, Existed: !table.Created},
	}, nil
}
```

NOTE: `ErrBootstrapNotImplemented` is deleted. Task 4 removes its last consumer in `cmd/setup`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/state/ -run TestBootstrap -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/state/bootstrap.go internal/state/bootstrap_test.go
git commit -m "feat(state): wire Bootstrap to real state backend creation"
```

---

### Task 4: `cmd/setup` confirmation + apply rewrite

**Files:**
- Modify: `cmd/setup/setup.go`
- Test: `cmd/setup/setup_test.go` (extend), `cmd/setup/cobra_test.go` (extend)

**Interfaces:**
- Consumes: `state.Bootstrap` (Task 3), `prompt.Confirm` (existing).
- Produces: updated `command` struct with a `confirm func(string) bool` seam and `assumeYes bool`.

- [ ] **Step 1: Write failing white-box tests in `cmd/setup/setup_test.go`**

Inspect the existing test file first for the `fakeProvider` + helper patterns. Add:

```go
func TestRunApplyConfirmYesCreates(t *testing.T) {
	var bootstrapCalled bool
	c := command{
		runtime:   testRuntime(t, "123"), // existing helper or build inline
		out:       &bytes.Buffer{},
		costs:     fabricacost.Global,
		assumeYes: false,
		confirm:   func(string) bool { return true },
		bootstrap: func(ctx context.Context, p fabricac.Provider, cfg *config.Config) ([]fabricastate.BootstrapResult, error) {
			bootstrapCalled = true
			return []fabricastate.BootstrapResult{{Name: "S3 bucket b"}, {Name: "DynamoDB table t"}}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !bootstrapCalled {
		t.Error("bootstrap should be called when confirmed")
	}
}

func TestRunApplyConfirmNoCancels(t *testing.T) {
	var bootstrapCalled bool
	c := command{
		runtime: testRuntime(t, "123"),
		out:     &bytes.Buffer{},
		costs:   fabricacost.Global,
		confirm: func(string) bool { return false },
		bootstrap: func(ctx context.Context, p fabricac.Provider, cfg *config.Config) ([]fabricastate.BootstrapResult, error) {
			bootstrapCalled = true
			return nil, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if bootstrapCalled {
		t.Error("bootstrap should NOT be called when declined")
	}
}

func TestRunApplyAssumeYesSkipsConfirm(t *testing.T) {
	confirmCalled := false
	c := command{
		runtime:   testRuntime(t, "123"),
		out:       &bytes.Buffer{},
		costs:     fabricacost.Global,
		assumeYes: true,
		confirm:   func(string) bool { confirmCalled = true; return true },
		bootstrap: func(ctx context.Context, p fabricac.Provider, cfg *config.Config) ([]fabricastate.BootstrapResult, error) {
			return []fabricastate.BootstrapResult{}, nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if confirmCalled {
		t.Error("confirm must be skipped when assumeYes is set")
	}
}

func TestRunApplyBootstrapErrorPropagates(t *testing.T) {
	c := command{
		runtime:   testRuntime(t, "123"),
		out:       &bytes.Buffer{},
		costs:     fabricacost.Global,
		assumeYes: true,
		confirm:   func(string) bool { return true },
		bootstrap: func(ctx context.Context, p fabricac.Provider, cfg *config.Config) ([]fabricastate.BootstrapResult, error) {
			return nil, errors.New("boom")
		},
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error to propagate")
	}
}
```

`testRuntime(t, account)` builds a `globals.Runtime` with a fake provider whose `Identity` returns the account; reuse the existing fake provider in the file. Keep the existing dry-run test passing.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/setup/ -run TestRunApply -v`
Expected: FAIL (struct fields `assumeYes`/`confirm` undefined).

- [ ] **Step 3: Update `command` struct + `New` in `cmd/setup/setup.go`**

Add fields:

```go
	assumeYes bool
	confirm   func(prompt string) bool
```

In `New`, wire them in the returned `command{...}`:

```go
		assumeYes: opts.AssumeYes,
		confirm:   prompt.Confirm,
```

Add import `"github.com/jpvelasco/fabrica/internal/prompt"`.

- [ ] **Step 4: Rewrite `runApply` and remove the not-implemented path**

```go
func (c command) runApply(ctx context.Context, plan fabricastate.SetupPlan) error {
	c.printApplyHeader(plan)

	if !c.assumeYes {
		if !c.confirm("Create these resources?") {
			fmt.Fprintln(c.out, "Setup cancelled. No AWS resources were created.")
			return nil
		}
	} else {
		fmt.Fprintln(c.out, "Proceeding without confirmation (--yes set).")
		fmt.Fprintln(c.out)
	}

	results, err := c.bootstrap(ctx, c.runtime.Provider, c.runtime.Config)
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	c.printBootstrapResults(results)
	c.saveAccountID(plan.Account)
	c.printCompletion(results)
	return nil
}
```

Delete `printNotImplementedWarning` and the `errors.Is(err, fabricastate.ErrBootstrapNotImplemented)` branch. Remove the now-unused `"errors"` import if nothing else uses it. Remove the `docs/setup-manual.md` reference. Update the cobra `Long` text to describe real provisioning (no "not yet implemented" wording) and the dry-run `printDryRun` trailing NOTE lines (replace with: "Run without --dry-run to create these resources.").

- [ ] **Step 5: Extend black-box `cmd/setup/cobra_test.go`**

Add a test that `--dry-run` still produces the plan output and makes no bootstrap call, and one that `--yes` drives a successful apply through the minimal root. Follow the existing minimal-root construction in the file.

- [ ] **Step 6: Run tests + vet**

Run: `go test ./cmd/setup/ -v && go vet ./cmd/setup/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/setup/
git commit -m "feat(setup): add confirmation and wire real bootstrap apply"
```

---

### Task 5: `cmd/status` aggregate command — core (no probe)

**Files:**
- Create: `cmd/status/status.go`
- Modify: `cmd/root/root.go` (register)
- Test: `cmd/status/status_test.go` (new)

**Interfaces:**
- Consumes: `globals.Runtime`/`RuntimeSource`/`OptionsSource`, `state.ReadStateOrNew`, `state.State`, `stateutil.ResourceByType`, `cloud.StateBackendChecker`, `cloud.ResourceClient`.
- Produces: `New(runtimeSource, optionsSource, out) *cobra.Command`; `command.run(ctx) error`; `StatusReport`/`StatusModule`/`StatusBackend`/`StatusSummary` JSON types.

- [ ] **Step 1: Write failing white-box tests in `cmd/status/status_test.go`**

```go
package status

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestRunEmptyState(t *testing.T) {
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		readState: func() (*fabricastate.State, error) { return fabricastate.NewState("123", "us-west-2"), nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "fabrica setup") {
		t.Errorf("empty state should suggest setup; got:\n%s", out.String())
	}
}

func TestRunReportsModules(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule("perforce", "p4-2024", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc"},
	})
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "perforce") || !strings.Contains(s, "ready") {
		t.Errorf("expected perforce ready line; got:\n%s", s)
	}
}

func TestRunJSONShape(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule("horde", "ami-1", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-h"},
	})
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		jsonOut:   true,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var report StatusReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	if len(report.Modules) != 1 || report.Modules[0].Name != "horde" {
		t.Errorf("unexpected report: %+v", report)
	}
	if report.Summary.ModuleCount != 1 || report.Summary.ResourceCount != 1 {
		t.Errorf("unexpected summary: %+v", report.Summary)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/status/ -v`
Expected: FAIL (package/command undefined).

- [ ] **Step 3: Implement `cmd/status/status.go`**

```go
// Package status implements `fabrica status`: a read-only aggregate overview of
// all provisioned modules plus state-backend health. It never mutates state —
// the per-module `<module> status` commands own the provisioning→ready transition.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const lineWidth = 64

// probePorts maps module name → readiness TCP port (used only with --probe).
var probePorts = map[string]int{
	"perforce":    1666,
	"horde":       5000,
	"workstation": 8443,
}

// StatusReport is the JSON view of the aggregate status.
type StatusReport struct {
	Backend StatusBackend   `json:"backend"`
	Modules []StatusModule  `json:"modules"`
	Summary StatusSummary   `json:"summary"`
}

type StatusBackend struct {
	Bucket       string `json:"bucket,omitempty"`
	BucketExists string `json:"bucketExists"` // "yes" | "no" | "unknown"
	Table        string `json:"table,omitempty"`
	TableExists  string `json:"tableExists"`
}

type StatusModule struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	Version       string `json:"version,omitempty"`
	ResourceCount int    `json:"resourceCount"`
	InstanceID    string `json:"instanceId,omitempty"`
	SGID          string `json:"sgId,omitempty"`
	InstanceState string `json:"instanceState,omitempty"`
	Probe         string `json:"probe,omitempty"` // "responding" | "unreachable" | ""
}

type StatusSummary struct {
	ModuleCount   int `json:"moduleCount"`
	ResourceCount int `json:"resourceCount"`
}

type command struct {
	runtime   globals.Runtime
	jsonOut   bool
	probe     bool
	out       io.Writer
	readState func() (*fabricastate.State, error)
	getResource func(ctx context.Context, r *cloud.Resource) error
	backend   cloud.StateBackendChecker
	probeTCP  func(address string) bool
}

func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var probe bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show health overview across all modules",
		Long: `Show an aggregate, read-only overview of every provisioned Fabrica module
(Perforce, Horde, Workstation) plus the state backend.

Reads the local state cache (.fabrica/state.json) and queries EC2 instance
state via Cloud Control. This command never modifies state.

Use --probe to additionally TCP-probe each module's readiness port. Probing
requires network reachability to the (private) instance IPs — typically a VPN
or in-VPC session — and is off by default.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:   rt,
				jsonOut:   opts.JSONOutput,
				probe:     probe,
				out:       out,
				readState: func() (*fabricastate.State, error) { return readState(rt) },
				probeTCP:  modstatusProbe,
			}
			if rt.Provider != nil {
				c.getResource = rt.Provider.Resources().Get
				if b, ok := rt.Provider.(cloud.StateBackendChecker); ok {
					c.backend = b
				}
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&probe, "probe", false, "TCP-probe each module's readiness port (requires VPN/in-VPC)")
	return cmd
}

func (c command) run(ctx context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	backend := c.checkBackend(ctx)
	modules := c.buildModules(ctx, st)

	report := StatusReport{
		Backend: backend,
		Modules: modules,
		Summary: StatusSummary{ModuleCount: len(modules), ResourceCount: st.ModuleCount()},
	}

	if c.jsonOut {
		return c.printJSON(report)
	}
	c.printText(report)
	return nil
}
```

Add helper functions in the same file:

```go
func (c command) checkBackend(ctx context.Context) StatusBackend {
	b := StatusBackend{BucketExists: "unknown", TableExists: "unknown"}
	if c.runtime.Config == nil {
		return b
	}
	b.Bucket = c.runtime.Config.State.Bucket
	b.Table = c.runtime.Config.State.Table
	if c.backend == nil {
		return b
	}
	if b.Bucket != "" {
		b.BucketExists = yesNo(c.backend.StateBucketExists(ctx, b.Bucket))
	}
	if b.Table != "" {
		b.TableExists = yesNo(c.backend.StateLockTableExists(ctx, b.Table))
	}
	return b
}

func yesNo(ok bool, err error) string {
	if err != nil {
		return "unknown"
	}
	if ok {
		return "yes"
	}
	return "no"
}

func (c command) buildModules(ctx context.Context, st *fabricastate.State) []StatusModule {
	out := make([]StatusModule, 0, len(st.Modules))
	for i := range st.Modules {
		m := &st.Modules[i]
		sm := StatusModule{
			Name:          m.Name,
			Status:        m.Status,
			Version:       m.Version,
			ResourceCount: len(m.Resources),
		}
		if sg, ok := stateutil.ResourceByType(m, "AWS::EC2::SecurityGroup"); ok {
			sm.SGID = sg.Identifier
		}
		if inst, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance"); ok {
			sm.InstanceID = inst.Identifier
			sm.InstanceState = c.liveInstanceState(ctx, inst.Identifier)
			if c.probe {
				sm.Probe = c.probeModule(m.Name, sm.InstanceID)
			}
		}
		out = append(out, sm)
	}
	return out
}

func (c command) liveInstanceState(ctx context.Context, instanceID string) string {
	if c.getResource == nil || instanceID == "" {
		return ""
	}
	r := &cloud.Resource{TypeName: "AWS::EC2::Instance", Identifier: instanceID}
	if err := c.getResource(ctx, r); err != nil {
		return ""
	}
	return parseInstanceState(r.ActualState)
}

func parseInstanceState(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var actual struct {
		State struct {
			Name string `json:"Name"`
		} `json:"State"`
		PrivateIPAddress string `json:"PrivateIpAddress"`
	}
	if err := json.Unmarshal(raw, &actual); err != nil {
		return ""
	}
	return actual.State.Name
}
```

NOTE: the probe needs the private IP. Task 6 adds `probeModule` + private-IP extraction. For Task 5, stub `probeModule` to return `""` and define `modstatusProbe` as a thin wrapper so the file compiles:

```go
func (c command) probeModule(module, instanceID string) string { return "" } // replaced in Task 6

func modstatusProbe(address string) bool { return false } // replaced in Task 6
```

Add the text/JSON printers:

```go
func (c command) printJSON(report StatusReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding status: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}

func (c command) printText(report StatusReport) {
	fmt.Fprintln(c.out, "Fabrica status")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	c.printBackend(report.Backend)
	fmt.Fprintln(c.out)

	if len(report.Modules) == 0 {
		fmt.Fprintln(c.out, "No modules provisioned yet.")
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Next steps:")
		fmt.Fprintln(c.out, "  fabrica setup                 Provision the state backend")
		fmt.Fprintln(c.out, "  fabrica perforce create       Provision Perforce Helix Core")
		fmt.Fprintln(c.out, "  fabrica horde create          Provision Unreal Horde")
		fmt.Fprintln(c.out, "  fabrica workstation create    Provision a cloud workstation")
		return
	}

	for _, m := range report.Modules {
		c.printModule(m)
	}
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "%d module(s) · %d resource(s)\n", report.Summary.ModuleCount, report.Summary.ResourceCount)
	c.printNextSteps(report.Modules)
}

func (c command) printBackend(b StatusBackend) {
	bucket := b.Bucket
	if bucket == "" {
		bucket = "(not configured)"
	}
	fmt.Fprintf(c.out, "  State bucket:  %s [%s]\n", bucket, b.BucketExists)
	table := b.Table
	if table == "" {
		table = "(not configured)"
	}
	fmt.Fprintf(c.out, "  Lock table:    %s [%s]\n", table, b.TableExists)
}

func (c command) printModule(m StatusModule) {
	line := fmt.Sprintf("  %-12s %-12s %d resource(s)", m.Name, m.Status, m.ResourceCount)
	if m.InstanceState != "" {
		line += fmt.Sprintf("  ec2:%s", m.InstanceState)
	}
	if m.Probe != "" {
		line += fmt.Sprintf("  probe:%s", m.Probe)
	}
	fmt.Fprintln(c.out, line)
}

func (c command) printNextSteps(modules []StatusModule) {
	var steps []string
	for _, m := range modules {
		if m.Status == "provisioning" {
			steps = append(steps, fmt.Sprintf("  fabrica %s status     Watch %s become ready", m.Name, m.Name))
		}
	}
	sort.Strings(steps)
	if len(steps) == 0 {
		return
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	for _, s := range steps {
		fmt.Fprintln(c.out, s)
	}
}

func readState(rt globals.Runtime) (*fabricastate.State, error) {
	account, region := "", ""
	if rt.Config != nil {
		account = rt.Config.Cloud.AWS.AccountID
		region = rt.Config.Cloud.AWS.Region
	}
	return fabricastate.ReadStateOrNew(account, region)
}
```

- [ ] **Step 4: Register in `cmd/root/root.go`**

Add import `"github.com/jpvelasco/fabrica/cmd/status"` and, after the doctor registration:

```go
	cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
```

- [ ] **Step 5: Run tests + vet + build**

Run: `go test ./cmd/status/ -v && go vet ./cmd/status/ && go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/status/ cmd/root/root.go
git commit -m "feat(status): add aggregate read-only status command"
```

---

### Task 6: `cmd/status` `--probe` support

**Files:**
- Modify: `cmd/status/status.go` (replace the two stubs)
- Test: `cmd/status/status_test.go` (add probe tests)

**Interfaces:**
- Consumes: `modstatus.DefaultProbeTCP` (existing), `getResource` seam, `probePorts` map.
- Produces: real `probeModule`; `modstatusProbe` replaced by `modstatus.DefaultProbeTCP`.

- [ ] **Step 1: Add failing probe tests in `cmd/status/status_test.go`**

```go
func TestRunProbeReachable(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule("perforce", "v", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
	})
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		probe:     true,
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"State":{"Name":"running"},"PrivateIpAddress":"10.0.0.5"}`)
			return nil
		},
		probeTCP: func(address string) bool {
			if address != "10.0.0.5:1666" {
				t.Errorf("probe address = %q", address)
			}
			return true
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "probe:responding") {
		t.Errorf("expected probe:responding; got:\n%s", out.String())
	}
}

func TestRunProbeUnreachable(t *testing.T) {
	st := fabricastate.NewState("123", "us-west-2")
	st.UpsertModule("horde", "v", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-h"},
	})
	out := &bytes.Buffer{}
	c := command{
		out:       out,
		probe:     true,
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"State":{"Name":"running"},"PrivateIpAddress":"10.0.0.9"}`)
			return nil
		},
		probeTCP: func(address string) bool { return false },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "probe:unreachable") {
		t.Errorf("expected probe:unreachable; got:\n%s", out.String())
	}
}
```

The `getResource` here must also surface the private IP to `probeModule`. Update `liveInstanceState` flow: have `buildModules` capture the private IP. Adjust the implementation accordingly in Step 2.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/status/ -run TestRunProbe -v`
Expected: FAIL (probe stub returns "").

- [ ] **Step 3: Replace the stubs and thread the private IP**

Replace `parseInstanceState` to also return the private IP, and rework `buildModules` to use it:

```go
func parseInstanceState(raw []byte) (state, privateIP string) {
	if len(raw) == 0 {
		return "", ""
	}
	var actual struct {
		State struct {
			Name string `json:"Name"`
		} `json:"State"`
		PrivateIPAddress string `json:"PrivateIpAddress"`
	}
	if err := json.Unmarshal(raw, &actual); err != nil {
		return "", ""
	}
	return actual.State.Name, actual.PrivateIPAddress
}
```

In `buildModules`, replace the instance branch:

```go
		if inst, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance"); ok {
			sm.InstanceID = inst.Identifier
			ecState, privateIP := c.liveInstance(ctx, inst.Identifier)
			sm.InstanceState = ecState
			if c.probe && privateIP != "" {
				sm.Probe = c.probeModule(m.Name, privateIP)
			}
		}
```

Replace `liveInstanceState` with `liveInstance`:

```go
func (c command) liveInstance(ctx context.Context, instanceID string) (state, privateIP string) {
	if c.getResource == nil || instanceID == "" {
		return "", ""
	}
	r := &cloud.Resource{TypeName: "AWS::EC2::Instance", Identifier: instanceID}
	if err := c.getResource(ctx, r); err != nil {
		return "", ""
	}
	return parseInstanceState(r.ActualState)
}
```

Replace the `probeModule` stub:

```go
func (c command) probeModule(module, privateIP string) string {
	port, ok := probePorts[module]
	if !ok || c.probeTCP == nil {
		return ""
	}
	if c.probeTCP(fmt.Sprintf("%s:%d", privateIP, port)) {
		return "responding"
	}
	return "unreachable"
}
```

Replace the `modstatusProbe` stub: delete it and set the seam default in `New` to `modstatus.DefaultProbeTCP`. Add import `"github.com/jpvelasco/fabrica/cmd/internal/modstatus"` and change `probeTCP: modstatusProbe` → `probeTCP: modstatus.DefaultProbeTCP`.

- [ ] **Step 4: Run tests + vet + build**

Run: `go test ./cmd/status/ -v && go vet ./cmd/status/ && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/status/
git commit -m "feat(status): add --probe TCP readiness checks"
```

---

### Task 7: `cmd/status` black-box cobra tests

**Files:**
- Test: `cmd/status/cobra_test.go` (new)

**Interfaces:**
- Consumes: `status.New`, a minimal root replicating `--json`/`--probe` flags.

- [ ] **Step 1: Write black-box tests**

Model on `cmd/perforce/status/status_test.go`'s black-box counterpart. Build a minimal root with persistent `--json` flag, add `status.New(...)`, execute with no state file present (empty state), assert exit 0 and that output mentions `fabrica setup`. Add a `--json` execution asserting valid JSON. Use a temp working dir (`t.Chdir(t.TempDir())`) so no real `.fabrica/state.json` is read.

```go
package status_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/status"
	"github.com/spf13/cobra"
)

func newTestRoot(out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{Use: "fabrica"}
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	runtimeSource := func() (globals.Runtime, error) { return globals.Runtime{}, nil }
	optionsSource := func() globals.Options { return opts }
	root.AddCommand(status.New(runtimeSource, optionsSource, out))
	return root
}

func TestStatusCobraEmpty(t *testing.T) {
	t.Chdir(t.TempDir())
	out := &bytes.Buffer{}
	root := newTestRoot(out)
	root.SetArgs([]string{"status"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "fabrica setup") {
		t.Errorf("expected setup hint; got:\n%s", out.String())
	}
}

func TestStatusCobraJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	out := &bytes.Buffer{}
	root := newTestRoot(out)
	root.SetArgs([]string{"status", "--json"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Errorf("expected JSON object; got:\n%s", out.String())
	}
}
```

NOTE: confirm `globals.Runtime{}` zero value is safe (nil Provider/Config) — the command guards `rt.Provider != nil` and `rt.Config == nil`. If `runtimeSource` returning a zero Runtime trips `Store.Require`, build the source inline as shown (not via Store).

- [ ] **Step 2: Run tests**

Run: `go test ./cmd/status/ -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/status/cobra_test.go
git commit -m "test(status): add black-box cobra tests"
```

---

### Task 8: Integration test for the AWS bootstrapper (opt-in)

**Files:**
- Create: `internal/cloud/aws/state_backend_integration_test.go`

**Interfaces:**
- Consumes: the real `awsProvider` via `newProvider`, real AWS creds from the environment.

- [ ] **Step 1: Write the build-tagged integration test**

```go
//go:build integration

// Integration test for the real AWS state backend bootstrapper.
//
// Run with:
//   FABRICA_INTEGRATION=1 FABRICA_INTEGRATION_ACCOUNT=<acct> AWS_REGION=us-west-2 \
//     go test -tags integration ./internal/cloud/aws/ -run TestIntegrationBootstrap -v
//
// It creates a uniquely-named S3 bucket and DynamoDB table, asserts their
// configuration, and unconditionally cleans both up via t.Cleanup.
package aws

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/internal/config"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

func TestIntegrationBootstrap(t *testing.T) {
	if os.Getenv("FABRICA_INTEGRATION") != "1" {
		t.Skip("set FABRICA_INTEGRATION=1 to run real-AWS integration tests")
	}
	wantAccount := os.Getenv("FABRICA_INTEGRATION_ACCOUNT")
	if wantAccount == "" {
		t.Fatal("FABRICA_INTEGRATION_ACCOUNT must be set to guard against the wrong account")
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-west-2"
	}

	cfg := config.Defaults()
	cfg.Cloud.AWS.Region = region
	prov, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}

	ctx := context.Background()
	account, _, _, err := prov.Identity(ctx)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if account != wantAccount {
		t.Fatalf("refusing to run: live account %s != FABRICA_INTEGRATION_ACCOUNT %s", account, wantAccount)
	}

	boot, ok := prov.(fabricac.StateBackendBootstrapper)
	if !ok {
		t.Fatal("provider does not implement StateBackendBootstrapper")
	}
	checker := prov.(fabricac.StateBackendChecker)
	destroyer := prov.(fabricac.StateBackendDestroyer)

	suffix := time.Now().UTC().Format("20060102150405")
	bucket := "fabrica-it-" + account + "-" + suffix
	table := "fabrica-it-lock-" + suffix

	t.Cleanup(func() {
		// Best-effort unconditional cleanup.
		_, _ = destroyer.DeleteStateBucket(context.Background(), bucket)
		_, _ = destroyer.DeleteStateLockTable(context.Background(), table)
	})

	if _, err := boot.EnsureStateBucket(ctx, bucket, region); err != nil {
		t.Fatalf("EnsureStateBucket: %v", err)
	}
	if _, err := boot.EnsureStateLockTable(ctx, table); err != nil {
		t.Fatalf("EnsureStateLockTable: %v", err)
	}

	ok1, err := checker.StateBucketExists(ctx, bucket)
	if err != nil || !ok1 {
		t.Fatalf("bucket should exist: ok=%v err=%v", ok1, err)
	}
	ok2, err := checker.StateLockTableExists(ctx, table)
	if err != nil || !ok2 {
		t.Fatalf("table should exist: ok=%v err=%v", ok2, err)
	}

	// Idempotency: a second call must not error and must report not-created.
	res, err := boot.EnsureStateBucket(ctx, bucket, region)
	if err != nil {
		t.Fatalf("second EnsureStateBucket: %v", err)
	}
	if res.Created {
		t.Errorf("second call should report Created=false")
	}
}
```

NOTE: `t.Cleanup` deletes the bucket only if empty; the bootstrapper writes no objects, so it is empty. If versioning leaves a delete marker, the bucket has no objects to begin with, so `DeleteBucket` succeeds. Document in the header that the test creates only empty resources.

- [ ] **Step 2: Verify it compiles under the tag and is skipped without it**

Run: `go build -tags integration ./internal/cloud/aws/ && go test ./internal/cloud/aws/ -run TestIntegrationBootstrap -v`
Expected: build succeeds; without the tag the test is not present (package builds), so the `-run` matches nothing — that is fine. Then: `go vet -tags integration ./internal/cloud/aws/`.

- [ ] **Step 3: Commit**

```bash
git add internal/cloud/aws/state_backend_integration_test.go
git commit -m "test(aws): add opt-in integration test for state backend bootstrap"
```

---

### Task 9: Docs + full verification

**Files:**
- Modify: `ROADMAP.md`, `CLAUDE.md`

- [ ] **Step 1: Update `CLAUDE.md`**

Replace the "Project Status" paragraph stating `fabrica setup` is a no-op / `ErrBootstrapNotImplemented` with the real behavior: setup creates the S3 bucket (versioning + encryption + public-access-block) and DynamoDB lock table, idempotently, behind a y/N confirmation (`--yes` skips), with dry-run + cost preview retained. Add a `cmd/status` row to the package-responsibilities table ("Aggregate read-only overview: backend health + per-module status; `--probe` opt-in; never writes state"). Note `StateBackendBootstrapper` alongside Checker/Destroyer in the `internal/cloud` description.

- [ ] **Step 2: Update `ROADMAP.md`**

Mark `fabrica setup` and `fabrica status` as implemented in the relevant phase/command tables.

- [ ] **Step 3: Full verification suite**

Run each and confirm clean:

```bash
gofmt -l .                       # expect: no output
go vet ./...                     # expect: no output
go build ./...                   # expect: success
go test ./...                    # expect: all PASS
golangci-lint run ./...          # expect: no issues
```

Fix any failures before proceeding. For coverage spot-check:

```bash
go test ./cmd/status/ ./cmd/setup/ ./internal/state/ ./internal/cloud/aws/ -cover
```

Expect ≥60% on each touched package.

- [ ] **Step 4: Commit docs**

```bash
git add CLAUDE.md ROADMAP.md
git commit -m "docs: mark setup + aggregate status implemented"
```

---

### Task 10: Push + PR + CI

- [ ] **Step 1: Push the branch**

```bash
git push -u origin feat/milestone-1-setup-status
```

- [ ] **Step 2: Open the PR**

```bash
gh pr create --title "feat: real fabrica setup + aggregate fabrica status (Milestone 1)" \
  --body "$(cat <<'EOF'
## Summary
- Wire `state.Bootstrap()` to create the S3 state bucket (versioning, encryption, public access block) + DynamoDB lock table via a new `StateBackendBootstrapper` capability interface.
- `fabrica setup` now really provisions, behind a y/N confirmation (`--yes` skips); dry-run + cost preview retained; idempotent.
- New read-only `fabrica status`: backend health + per-module status, resource counts, actionable next steps, `--json`, `--probe` opt-in.

## Testing
- Hermetic mocked unit tests (default + CI) for bootstrapper, Bootstrap, setup, status.
- Opt-in `//go:build integration` real-AWS test with account guard + unconditional cleanup.
- `gofmt`, `go vet`, `go test ./...`, `golangci-lint` all clean locally.

Spec: docs/superpowers/specs/2026-06-27-milestone-1-setup-status-design.md
Plan: docs/superpowers/plans/2026-06-27-milestone-1-setup-status.md
EOF
)"
```

- [ ] **Step 3: Watch CI**

```bash
gh pr checks --watch
```

Expected: all checks pass. If a check fails, diagnose from the logs (`gh run view --log-failed`), fix, commit, push, re-watch. Do not merge with failing checks.

- [ ] **Step 4: Address reviewer comments**

Check for any review comments (`gh pr view --comments`); address (fix + reply) or respond why not, per workflow rules. Then leave the PR ready for the user to merge.

---

## Self-Review

**Spec coverage:**
- Real setup (bucket versioning/encryption/PAB + table) → Tasks 1–4. ✓
- Dry-run / cost / idempotency / confirmation → Task 4 (dry-run + cost retained from existing code; confirmation added; idempotency from Ensure* in Task 2). ✓
- Integration tests with cleanup → Task 8. ✓
- Aggregate status (overall status, resource counts, next steps) → Tasks 5–7. ✓
- Established patterns (seams, two-package tests, dry-run, error messaging) → throughout; black-box cobra tests in Tasks 4, 7. ✓
- Docs/ROADMAP/CLAUDE → Task 9. ✓
- Branch + PR + CI → Task 10. ✓

**Placeholder scan:** No TBD/TODO. Each code step shows full code. The Task 5 stubs are explicitly temporary and replaced in Task 6 (called out inline). ✓

**Type consistency:**
- `StateBackendCreateResult{Identifier, Created}` consistent across Tasks 1, 2, 3. ✓
- `EnsureStateBucket(ctx, bucket, region)` / `EnsureStateLockTable(ctx, table)` consistent Tasks 1–3, 8. ✓
- `command.confirm func(string) bool` matches `prompt.Confirm(msg string) bool`. ✓
- status `parseInstanceState` returns `(state, privateIP string)` after Task 6 rework — both call sites updated. ✓
- `probeModule(module, privateIP string)` final signature consistent with its call site in Task 6. ✓
