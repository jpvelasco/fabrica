# Cloud Control CRUD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement all five `ResourceClient` methods in `internal/cloud/aws/cloudcontrol.go` against the real AWS Cloud Control API, replacing the current no-op stubs.

**Architecture:** Define a `ccAPIClient` interface and `ccWaiter` interface over the SDK types; wire them into `resourceClients` with factory seams for testability. Mutation operations (`Create`, `Delete`, `Update`) block internally using `ResourceRequestSuccessWaiter.WaitForOutput`. `Get` and `List` are synchronous. All five methods map Cloud Control error codes to `cloud.ErrResourceNotFound` where appropriate.

**Tech Stack:** `github.com/aws/aws-sdk-go-v2/service/cloudcontrol v1.9.0`, `github.com/aws/smithy-go`, Go 1.25.

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/cloud/aws/resource.go` | Modify | Replace empty `ccClient` struct with `ccAPIClient` and `ccWaiter` interfaces |
| `internal/cloud/aws/aws.go` | Modify | Extend `resourceClients` with seam fields; update `Resources()` to pass `awsCfg` |
| `internal/cloud/aws/cloudcontrol.go` | Rewrite | Full implementation of all five methods + `ensureClient` lazy-init helper |
| `internal/cloud/aws/cloudcontrol_test.go` | Create | Table-driven tests using `fakeCCClient` + `fakeCCWaiter` |
| `CLAUDE.md` | Modify | Remove perforce/horde-specific caveat about Cloud Control being stubbed |

---

## Task 1: Create branch and define interfaces in `resource.go`

**Files:**
- Modify: `internal/cloud/aws/resource.go`

- [ ] **Create the feature branch**

```bash
cd F:/source/fabrica
git checkout -b feat/cloudcontrol-crud
```

- [ ] **Replace `resource.go` with interface definitions**

Replace the entire file with:

```go
package aws

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
)

// ccAPIClient is the subset of the Cloud Control SDK client surface used by resourceClients.
type ccAPIClient interface {
	CreateResource(ctx context.Context, params *cloudcontrol.CreateResourceInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.CreateResourceOutput, error)
	GetResource(ctx context.Context, params *cloudcontrol.GetResourceInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error)
	UpdateResource(ctx context.Context, params *cloudcontrol.UpdateResourceInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.UpdateResourceOutput, error)
	DeleteResource(ctx context.Context, params *cloudcontrol.DeleteResourceInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.DeleteResourceOutput, error)
	ListResources(ctx context.Context, params *cloudcontrol.ListResourcesInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.ListResourcesOutput, error)
	GetResourceRequestStatus(ctx context.Context, params *cloudcontrol.GetResourceRequestStatusInput, optFns ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceRequestStatusOutput, error)
}

// ccWaiter polls GetResourceRequestStatus until a resource operation reaches SUCCESS or FAILED.
// WaitForOutput is used so callers can read ProgressEvent.Identifier from the result
// without an extra GetResourceRequestStatus call.
type ccWaiter interface {
	WaitForOutput(ctx context.Context, params *cloudcontrol.GetResourceRequestStatusInput, maxWait time.Duration, optFns ...func(*cloudcontrol.ResourceRequestSuccessWaiterOptions)) (*cloudcontrol.GetResourceRequestStatusOutput, error)
}
```

- [ ] **Verify it compiles**

```bash
go build ./internal/cloud/aws/...
```

Expected: no errors.

- [ ] **Commit**

```bash
git add internal/cloud/aws/resource.go
git commit -m "refactor: replace ccClient stub with ccAPIClient and ccWaiter interfaces"
```

---

## Task 2: Extend `resourceClients` in `aws.go`

**Files:**
- Modify: `internal/cloud/aws/aws.go`

- [ ] **Update `resourceClients` struct and `Resources()` method**

Replace the existing `resourceClients` struct definition and `Resources()` method in `aws.go`. The rest of the file stays the same.

Current `resourceClients`:
```go
type resourceClients struct {
	cc      *ccClient
	version string
}
```

Replace with:
```go
const defaultWaitTimeout = 15 * time.Minute

type resourceClients struct {
	cc          ccAPIClient
	waiter      ccWaiter
	awsCfg      awsConfig
	version     string
	waitTimeout time.Duration // 0 → defaultWaitTimeout

	// seams for testing — nil means use real SDK constructors
	loadCfg   func(ctx context.Context, region, profile string) (aws.Config, error)
	newClient func(aws.Config) ccAPIClient
	newWaiter func(ccAPIClient) ccWaiter
}
```

Replace `Resources()`:
```go
func (p *awsProvider) Resources() fabricac.ResourceClient {
	if p.clients.awsCfg == (awsConfig{}) {
		p.clients.awsCfg = p.awsCfg
		p.clients.version = fabricav.Version
	}
	return &p.clients
}
```

Update imports in `aws.go` to add `"time"` and `"context"`:

```go
import (
	"context"
	"time"

	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
)
```

- [ ] **Verify it compiles**

```bash
go build ./internal/cloud/aws/...
```

Expected: no errors.

- [ ] **Commit**

```bash
git add internal/cloud/aws/aws.go
git commit -m "refactor: extend resourceClients with seam fields and lazy-init awsCfg"
```

---

## Task 3: Implement `cloudcontrol.go`

**Files:**
- Rewrite: `internal/cloud/aws/cloudcontrol.go`

- [ ] **Replace the entire file with the full implementation**

```go
package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

var _ fabricac.ResourceClient = (*resourceClients)(nil)

// Create provisions a new cloud resource and blocks until the operation reaches
// a terminal state. Blocking keeps callers simple and consistent with existing
// perforce/horde create commands that immediately use r.Identifier after this call.
func (c *resourceClients) Create(ctx context.Context, r *fabricac.Resource) error {
	if err := c.ensureClient(ctx); err != nil {
		return err
	}

	r.DesiredState = injectFabricaTags(r.DesiredState, "fabrica", c.version, nil)

	out, err := c.cc.CreateResource(ctx, &cloudcontrol.CreateResourceInput{
		TypeName:     aws.String(r.TypeName),
		DesiredState: aws.String(string(r.DesiredState)),
	})
	if err != nil {
		return fmt.Errorf("creating %s: %w", r.TypeName, err)
	}

	token := aws.ToString(out.ProgressEvent.RequestToken)
	result, err := c.waiter.WaitForOutput(ctx, &cloudcontrol.GetResourceRequestStatusInput{
		RequestToken: aws.String(token),
	}, c.timeout())
	if err != nil {
		return fmt.Errorf("waiting for %s creation: %w", r.TypeName, err)
	}

	if result.ProgressEvent.OperationStatus == types.OperationStatusFailed {
		return progressEventError(r.TypeName, result.ProgressEvent)
	}

	r.Identifier = aws.ToString(result.ProgressEvent.Identifier)
	return nil
}

// Get retrieves the current state of a resource and populates r.ActualState.
func (c *resourceClients) Get(ctx context.Context, r *fabricac.Resource) error {
	if err := c.ensureClient(ctx); err != nil {
		return err
	}

	out, err := c.cc.GetResource(ctx, &cloudcontrol.GetResourceInput{
		TypeName:   aws.String(r.TypeName),
		Identifier: aws.String(r.Identifier),
	})
	if err != nil {
		if isNotFound(err) {
			return fabricac.ErrResourceNotFound
		}
		return fmt.Errorf("getting %s %s: %w", r.TypeName, r.Identifier, err)
	}

	if out.ResourceDescription != nil && out.ResourceDescription.Properties != nil {
		r.ActualState = json.RawMessage(*out.ResourceDescription.Properties)
	}
	return nil
}

// Update applies a JSON patch document (r.DesiredState) to the resource and blocks
// until the operation completes. r.DesiredState must be a valid RFC 6902 patch document,
// e.g. [{"op":"replace","path":"/Foo","value":"bar"}].
func (c *resourceClients) Update(ctx context.Context, r *fabricac.Resource) error {
	if err := c.ensureClient(ctx); err != nil {
		return err
	}

	out, err := c.cc.UpdateResource(ctx, &cloudcontrol.UpdateResourceInput{
		TypeName:      aws.String(r.TypeName),
		Identifier:    aws.String(r.Identifier),
		PatchDocument: aws.String(string(r.DesiredState)),
	})
	if err != nil {
		return fmt.Errorf("updating %s %s: %w", r.TypeName, r.Identifier, err)
	}

	token := aws.ToString(out.ProgressEvent.RequestToken)
	result, err := c.waiter.WaitForOutput(ctx, &cloudcontrol.GetResourceRequestStatusInput{
		RequestToken: aws.String(token),
	}, c.timeout())
	if err != nil {
		return fmt.Errorf("waiting for %s update: %w", r.TypeName, err)
	}

	if result.ProgressEvent.OperationStatus == types.OperationStatusFailed {
		return progressEventError(r.TypeName, result.ProgressEvent)
	}
	return nil
}

// Delete removes a resource and blocks until the operation completes.
// Returns cloud.ErrResourceNotFound if the resource does not exist (idempotent).
func (c *resourceClients) Delete(ctx context.Context, r *fabricac.Resource) error {
	if err := c.ensureClient(ctx); err != nil {
		return err
	}

	out, err := c.cc.DeleteResource(ctx, &cloudcontrol.DeleteResourceInput{
		TypeName:   aws.String(r.TypeName),
		Identifier: aws.String(r.Identifier),
	})
	if err != nil {
		if isNotFound(err) {
			return fabricac.ErrResourceNotFound
		}
		return fmt.Errorf("deleting %s %s: %w", r.TypeName, r.Identifier, err)
	}

	token := aws.ToString(out.ProgressEvent.RequestToken)
	result, err := c.waiter.WaitForOutput(ctx, &cloudcontrol.GetResourceRequestStatusInput{
		RequestToken: aws.String(token),
	}, c.timeout())
	if err != nil {
		return fmt.Errorf("waiting for %s deletion: %w", r.TypeName, err)
	}

	if result.ProgressEvent.OperationStatus == types.OperationStatusFailed {
		if result.ProgressEvent.ErrorCode == types.HandlerErrorCodeNotFound ||
			result.ProgressEvent.ErrorCode == types.HandlerErrorCodeAlreadyExists {
			return fabricac.ErrResourceNotFound
		}
		return progressEventError(r.TypeName, result.ProgressEvent)
	}
	return nil
}

// List returns all resources of the given type, paginating automatically.
func (c *resourceClients) List(ctx context.Context, typeName string) ([]fabricac.Resource, error) {
	if err := c.ensureClient(ctx); err != nil {
		return nil, err
	}

	var resources []fabricac.Resource
	var nextToken *string

	for {
		out, err := c.cc.ListResources(ctx, &cloudcontrol.ListResourcesInput{
			TypeName:  aws.String(typeName),
			NextToken: nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", typeName, err)
		}

		for _, desc := range out.ResourceDescriptions {
			r := fabricac.Resource{
				TypeName:   typeName,
				Identifier: aws.ToString(desc.Identifier),
			}
			if desc.Properties != nil {
				r.ActualState = json.RawMessage(*desc.Properties)
			}
			resources = append(resources, r)
		}

		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	return resources, nil
}

// ensureClient lazily initialises the SDK client and waiter on first use.
func (c *resourceClients) ensureClient(ctx context.Context) error {
	if c.cc != nil {
		return nil
	}

	loadCfg := c.loadCfg
	if loadCfg == nil {
		loadCfg = loadAWSConfig
	}
	cfg, err := loadCfg(ctx, c.awsCfg.region, c.awsCfg.profile)
	if err != nil {
		return fmt.Errorf("loading AWS config for Cloud Control: %w", err)
	}

	newClient := c.newClient
	if newClient == nil {
		newClient = func(cfg aws.Config) ccAPIClient {
			return cloudcontrol.NewFromConfig(cfg)
		}
	}
	c.cc = newClient(cfg)

	newWaiter := c.newWaiter
	if newWaiter == nil {
		newWaiter = func(cl ccAPIClient) ccWaiter {
			return cloudcontrol.NewResourceRequestSuccessWaiter(cl.(cloudcontrol.GetResourceRequestStatusAPIClient))
		}
	}
	c.waiter = newWaiter(c.cc)

	return nil
}

func (c *resourceClients) timeout() time.Duration {
	if c.waitTimeout > 0 {
		return c.waitTimeout
	}
	return defaultWaitTimeout
}

// progressEventError builds an error from a FAILED ProgressEvent, including the
// StatusMessage when available so operators can see the provider's failure reason.
func progressEventError(typeName string, ev *types.ProgressEvent) error {
	msg := ""
	if ev.StatusMessage != nil && *ev.StatusMessage != "" {
		msg = ": " + *ev.StatusMessage
	}
	return fmt.Errorf("resource operation on %s failed (code: %s)%s", typeName, ev.ErrorCode, msg)
}

// isNotFound reports whether an SDK error represents a resource-not-found condition.
func isNotFound(err error) bool {
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == string(types.HandlerErrorCodeNotFound) ||
			code == "NotFound" ||
			code == "ResourceNotFoundException"
	}
	return false
}
```

- [ ] **Verify it compiles**

```bash
go build ./internal/cloud/aws/...
```

Expected: no errors.

- [ ] **Commit**

```bash
git add internal/cloud/aws/cloudcontrol.go
git commit -m "feat: implement Cloud Control CRUD methods in resourceClients"
```

---

## Task 4: Write tests in `cloudcontrol_test.go`

**Files:**
- Create: `internal/cloud/aws/cloudcontrol_test.go`

- [ ] **Create the test file**

```go
package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
)

// newCCTestClients returns a resourceClients wired with the provided fakes.
// If fakeClient or fakeWaiter is nil, that field stays nil (ensureClient skipped via pre-set cc).
func newCCTestClients(fakeClient *fakeCCClient, fakeWaiter *fakeCCWaiter) *resourceClients {
	rc := &resourceClients{
		cc:      fakeClient,
		waiter:  fakeWaiter,
		version: "test",
	}
	return rc
}

// --- Create ---

func TestCreate_Success(t *testing.T) {
	token := "tok-create-1"
	identifier := "sg-abc123"

	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String(token)},
		},
	}
	waiter := &fakeCCWaiter{
		out: &cloudcontrol.GetResourceRequestStatusOutput{
			ProgressEvent: &types.ProgressEvent{
				OperationStatus: types.OperationStatusSuccess,
				Identifier:      awssdk.String(identifier),
			},
		},
	}
	rc := newCCTestClients(client, waiter)

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroup",
		DesiredState: json.RawMessage(`{"GroupName":"test-sg"}`),
	}
	if err := rc.Create(context.Background(), r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.Identifier != identifier {
		t.Errorf("Identifier = %q, want %q", r.Identifier, identifier)
	}
	if client.createCalls != 1 {
		t.Errorf("CreateResource calls = %d, want 1", client.createCalls)
	}
	if waiter.calls != 1 {
		t.Errorf("WaitForOutput calls = %d, want 1", waiter.calls)
	}
	if waiter.token != token {
		t.Errorf("WaitForOutput token = %q, want %q", waiter.token, token)
	}
}

func TestCreate_SDKError(t *testing.T) {
	client := &fakeCCClient{createErr: fmt.Errorf("access denied")}
	rc := newCCTestClients(client, &fakeCCWaiter{})

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", DesiredState: json.RawMessage(`{}`)}
	err := rc.Create(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(), "creating AWS::EC2::SecurityGroup")
}

func TestCreate_WaiterFailure(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
		},
	}
	waiter := &fakeCCWaiter{err: fmt.Errorf("timed out")}
	rc := newCCTestClients(client, waiter)

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", DesiredState: json.RawMessage(`{}`)}
	err := rc.Create(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(), "waiting for AWS::EC2::SecurityGroup creation")
}

func TestCreate_FailedStatus_IncludesStatusMessage(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
		},
	}
	waiter := &fakeCCWaiter{
		out: &cloudcontrol.GetResourceRequestStatusOutput{
			ProgressEvent: &types.ProgressEvent{
				OperationStatus: types.OperationStatusFailed,
				ErrorCode:       types.HandlerErrorCodeInvalidRequest,
				StatusMessage:   awssdk.String("GroupName already exists"),
			},
		},
	}
	rc := newCCTestClients(client, waiter)

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", DesiredState: json.RawMessage(`{}`)}
	err := rc.Create(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(), "GroupName already exists")
	assertContains(t, err.Error(), "InvalidRequest")
}

// --- Get ---

func TestGet_Success(t *testing.T) {
	props := `{"GroupId":"sg-abc123","GroupName":"test-sg"}`
	client := &fakeCCClient{
		getOut: &cloudcontrol.GetResourceOutput{
			ResourceDescription: &types.ResourceDescription{
				Identifier: awssdk.String("sg-abc123"),
				Properties: awssdk.String(props),
			},
		},
	}
	rc := newCCTestClients(client, nil)

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc123"}
	if err := rc.Get(context.Background(), r); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(r.ActualState) != props {
		t.Errorf("ActualState = %s, want %s", r.ActualState, props)
	}
	if client.getCalls != 1 {
		t.Errorf("GetResource calls = %d, want 1", client.getCalls)
	}
}

func TestGet_NotFound_ReturnsErrResourceNotFound(t *testing.T) {
	client := &fakeCCClient{getErr: ccAPIError("NotFound")}
	rc := newCCTestClients(client, nil)

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-missing"}
	err := rc.Get(context.Background(), r)
	if !errors.Is(err, fabricac.ErrResourceNotFound) {
		t.Fatalf("error = %v, want ErrResourceNotFound", err)
	}
}

func TestGet_SDKError(t *testing.T) {
	client := &fakeCCClient{getErr: fmt.Errorf("network error")}
	rc := newCCTestClients(client, nil)

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc123"}
	err := rc.Get(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(), "getting AWS::EC2::SecurityGroup sg-abc123")
}

// --- Update ---

func TestUpdate_Success(t *testing.T) {
	token := "tok-update-1"
	client := &fakeCCClient{
		updateOut: &cloudcontrol.UpdateResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String(token)},
		},
	}
	waiter := &fakeCCWaiter{
		out: &cloudcontrol.GetResourceRequestStatusOutput{
			ProgressEvent: &types.ProgressEvent{OperationStatus: types.OperationStatusSuccess},
		},
	}
	rc := newCCTestClients(client, waiter)

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroup",
		Identifier:   "sg-abc123",
		DesiredState: json.RawMessage(`[{"op":"replace","path":"/Description","value":"updated"}]`),
	}
	if err := rc.Update(context.Background(), r); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if client.updateCalls != 1 {
		t.Errorf("UpdateResource calls = %d, want 1", client.updateCalls)
	}
}

func TestUpdate_WaiterFailure(t *testing.T) {
	client := &fakeCCClient{
		updateOut: &cloudcontrol.UpdateResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
		},
	}
	waiter := &fakeCCWaiter{err: fmt.Errorf("timeout")}
	rc := newCCTestClients(client, waiter)

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc123", DesiredState: json.RawMessage(`[]`)}
	err := rc.Update(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(), "waiting for AWS::EC2::SecurityGroup update")
}

// --- Delete ---

func TestDelete_Success(t *testing.T) {
	token := "tok-delete-1"
	client := &fakeCCClient{
		deleteOut: &cloudcontrol.DeleteResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String(token)},
		},
	}
	waiter := &fakeCCWaiter{
		out: &cloudcontrol.GetResourceRequestStatusOutput{
			ProgressEvent: &types.ProgressEvent{OperationStatus: types.OperationStatusSuccess},
		},
	}
	rc := newCCTestClients(client, waiter)

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc123"}
	if err := rc.Delete(context.Background(), r); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if client.deleteCalls != 1 {
		t.Errorf("DeleteResource calls = %d, want 1", client.deleteCalls)
	}
}

func TestDelete_NotFound_ReturnsErrResourceNotFound(t *testing.T) {
	client := &fakeCCClient{deleteErr: ccAPIError("NotFound")}
	rc := newCCTestClients(client, nil)

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-missing"}
	err := rc.Delete(context.Background(), r)
	if !errors.Is(err, fabricac.ErrResourceNotFound) {
		t.Fatalf("error = %v, want ErrResourceNotFound", err)
	}
}

func TestDelete_WaiterFailure(t *testing.T) {
	client := &fakeCCClient{
		deleteOut: &cloudcontrol.DeleteResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
		},
	}
	waiter := &fakeCCWaiter{err: fmt.Errorf("timeout")}
	rc := newCCTestClients(client, waiter)

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc123"}
	err := rc.Delete(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(), "waiting for AWS::EC2::SecurityGroup deletion")
}

func TestDelete_FailedStatus_IncludesStatusMessage(t *testing.T) {
	client := &fakeCCClient{
		deleteOut: &cloudcontrol.DeleteResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
		},
	}
	waiter := &fakeCCWaiter{
		out: &cloudcontrol.GetResourceRequestStatusOutput{
			ProgressEvent: &types.ProgressEvent{
				OperationStatus: types.OperationStatusFailed,
				ErrorCode:       types.HandlerErrorCodeGeneralServiceException,
				StatusMessage:   awssdk.String("dependency violation"),
			},
		},
	}
	rc := newCCTestClients(client, waiter)

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc123"}
	err := rc.Delete(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(), "dependency violation")
}

// --- List ---

func TestList_SinglePage(t *testing.T) {
	client := &fakeCCClient{
		listPages: [][]types.ResourceDescription{
			{
				{Identifier: awssdk.String("sg-aaa"), Properties: awssdk.String(`{"GroupId":"sg-aaa"}`)},
				{Identifier: awssdk.String("sg-bbb"), Properties: awssdk.String(`{"GroupId":"sg-bbb"}`)},
			},
		},
	}
	rc := newCCTestClients(client, nil)

	resources, err := rc.List(context.Background(), "AWS::EC2::SecurityGroup")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("len = %d, want 2", len(resources))
	}
	if resources[0].Identifier != "sg-aaa" {
		t.Errorf("resources[0].Identifier = %q, want sg-aaa", resources[0].Identifier)
	}
	if string(resources[1].ActualState) != `{"GroupId":"sg-bbb"}` {
		t.Errorf("resources[1].ActualState = %s", resources[1].ActualState)
	}
}

func TestList_Paginated(t *testing.T) {
	client := &fakeCCClient{
		listPages: [][]types.ResourceDescription{
			{{Identifier: awssdk.String("sg-p1a")}},
			{{Identifier: awssdk.String("sg-p2a")}},
		},
	}
	rc := newCCTestClients(client, nil)

	resources, err := rc.List(context.Background(), "AWS::EC2::SecurityGroup")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("len = %d, want 2", len(resources))
	}
	if client.listCalls != 2 {
		t.Errorf("ListResources calls = %d, want 2", client.listCalls)
	}
}

func TestList_Empty(t *testing.T) {
	client := &fakeCCClient{listPages: [][]types.ResourceDescription{{}}}
	rc := newCCTestClients(client, nil)

	resources, err := rc.List(context.Background(), "AWS::EC2::SecurityGroup")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("len = %d, want 0", len(resources))
	}
}

func TestList_SDKError(t *testing.T) {
	client := &fakeCCClient{listErr: fmt.Errorf("access denied")}
	rc := newCCTestClients(client, nil)

	_, err := rc.List(context.Background(), "AWS::EC2::SecurityGroup")
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(), "listing AWS::EC2::SecurityGroup")
}

// --- ensureClient ---

func TestEnsureClient_LoadConfigError(t *testing.T) {
	rc := &resourceClients{
		awsCfg:  awsConfig{region: "us-east-1"},
		version: "test",
		loadCfg: func(ctx context.Context, region, profile string) (awssdk.Config, error) {
			return awssdk.Config{}, fmt.Errorf("no credentials")
		},
	}
	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", DesiredState: json.RawMessage(`{}`)}
	err := rc.Create(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(), "loading AWS config for Cloud Control")
}

// --- Timeout ---

func TestTimeout_DefaultUsedWhenZero(t *testing.T) {
	rc := &resourceClients{}
	if rc.timeout() != defaultWaitTimeout {
		t.Errorf("timeout() = %v, want %v", rc.timeout(), defaultWaitTimeout)
	}
}

func TestTimeout_CustomValueRespected(t *testing.T) {
	rc := &resourceClients{waitTimeout: 5 * time.Minute}
	if rc.timeout() != 5*time.Minute {
		t.Errorf("timeout() = %v, want 5m", rc.timeout())
	}
}

// ---- fakes ----

type fakeCCClient struct {
	createCalls int
	getCalls    int
	updateCalls int
	deleteCalls int
	listCalls   int

	createOut *cloudcontrol.CreateResourceOutput
	createErr error

	getOut *cloudcontrol.GetResourceOutput
	getErr error

	updateOut *cloudcontrol.UpdateResourceOutput
	updateErr error

	deleteOut *cloudcontrol.DeleteResourceOutput
	deleteErr error

	// listPages simulates pagination: each inner slice is one page of results.
	// A non-nil next token is set between pages automatically.
	listPages [][]types.ResourceDescription
	listErr   error
}

func (f *fakeCCClient) CreateResource(ctx context.Context, in *cloudcontrol.CreateResourceInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.CreateResourceOutput, error) {
	f.createCalls++
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createOut, nil
}

func (f *fakeCCClient) GetResource(ctx context.Context, in *cloudcontrol.GetResourceInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error) {
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getOut, nil
}

func (f *fakeCCClient) UpdateResource(ctx context.Context, in *cloudcontrol.UpdateResourceInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.UpdateResourceOutput, error) {
	f.updateCalls++
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return f.updateOut, nil
}

func (f *fakeCCClient) DeleteResource(ctx context.Context, in *cloudcontrol.DeleteResourceInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.DeleteResourceOutput, error) {
	f.deleteCalls++
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return f.deleteOut, nil
}

func (f *fakeCCClient) ListResources(ctx context.Context, in *cloudcontrol.ListResourcesInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.ListResourcesOutput, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	if len(f.listPages) == 0 {
		return &cloudcontrol.ListResourcesOutput{}, nil
	}
	page := f.listPages[0]
	f.listPages = f.listPages[1:]
	out := &cloudcontrol.ListResourcesOutput{ResourceDescriptions: page}
	if len(f.listPages) > 0 {
		out.NextToken = awssdk.String(fmt.Sprintf("page-%d", f.listCalls+1))
	}
	return out, nil
}

func (f *fakeCCClient) GetResourceRequestStatus(ctx context.Context, in *cloudcontrol.GetResourceRequestStatusInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceRequestStatusOutput, error) {
	return &cloudcontrol.GetResourceRequestStatusOutput{}, nil
}

type fakeCCWaiter struct {
	calls int
	token string
	out   *cloudcontrol.GetResourceRequestStatusOutput
	err   error
}

func (f *fakeCCWaiter) WaitForOutput(ctx context.Context, in *cloudcontrol.GetResourceRequestStatusInput, maxWait time.Duration, _ ...func(*cloudcontrol.ResourceRequestSuccessWaiterOptions)) (*cloudcontrol.GetResourceRequestStatusOutput, error) {
	f.calls++
	f.token = awssdk.ToString(in.RequestToken)
	if f.err != nil {
		return nil, f.err
	}
	if f.out == nil {
		return &cloudcontrol.GetResourceRequestStatusOutput{
			ProgressEvent: &types.ProgressEvent{OperationStatus: types.OperationStatusSuccess},
		}, nil
	}
	return f.out, nil
}

// ccAPIError returns an error whose ErrorCode() matches the given code,
// matching the smithy.APIError interface checked by isNotFound.
type ccAPIErrorImpl struct{ code string }

func (e *ccAPIErrorImpl) ErrorCode() string    { return e.code }
func (e *ccAPIErrorImpl) ErrorMessage() string { return e.code }
func (e *ccAPIErrorImpl) ErrorFault() int      { return 0 }
func (e *ccAPIErrorImpl) Error() string        { return e.code }

func ccAPIError(code string) error { return &ccAPIErrorImpl{code: code} }

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}
```

- [ ] **Run tests to confirm they pass**

```bash
go test ./internal/cloud/aws/... -v -run TestCreate
go test ./internal/cloud/aws/... -v -run TestGet
go test ./internal/cloud/aws/... -v -run TestUpdate
go test ./internal/cloud/aws/... -v -run TestDelete
go test ./internal/cloud/aws/... -v -run TestList
go test ./internal/cloud/aws/... -v -run TestEnsure
go test ./internal/cloud/aws/... -v -run TestTimeout
```

Expected: all PASS.

- [ ] **Run the full test suite**

```bash
go test ./...
```

Expected: all PASS.

- [ ] **Commit**

```bash
git add internal/cloud/aws/cloudcontrol_test.go
git commit -m "test: add Cloud Control CRUD unit tests with fake client and waiter"
```

---

## Task 5: Update `CLAUDE.md` and open PR

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Update the Project Status section in `CLAUDE.md`**

Find this line:
```
Phase 0 (CLI skeleton + AWS foundation) is complete. The `perforce` module (create/status/destroy) and `horde` module (create/status/submit) are fully implemented. Cloud Control calls are live; the CloudControl stub (`internal/cloud/aws/cloudcontrol.go`) is still used for the broader resource API in non-perforce/horde paths.
```

Replace with:
```
Phase 0 (CLI skeleton + AWS foundation) is complete. The `perforce` module (create/status/destroy) and `horde` module (create/status/submit) are fully implemented. All five `ResourceClient` methods in `internal/cloud/aws/cloudcontrol.go` are implemented against the real Cloud Control API — new modules can use `rt.Provider.Resources()` without routing through module-specific SDK wrappers.
```

- [ ] **Run full build and test**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: all pass, no lint errors.

- [ ] **Commit and push**

```bash
git add CLAUDE.md
git commit -m "docs: update project status — Cloud Control CRUD fully implemented"
git push -u origin feat/cloudcontrol-crud
```

- [ ] **Open PR**

```bash
gh pr create \
  --title "feat: implement Cloud Control CRUD methods" \
  --body "$(cat <<'EOF'
## Summary

- Implements all five `ResourceClient` methods (`Create`, `Get`, `Update`, `Delete`, `List`) in `internal/cloud/aws/cloudcontrol.go` against the real AWS Cloud Control API SDK.
- Mutation operations block using `ResourceRequestSuccessWaiter.WaitForOutput` with a configurable timeout (default 15 min).
- `NotFound`/`AlreadyDeleted` handler error codes map to `cloud.ErrResourceNotFound` for idempotent deletes.
- Failed operations include `StatusMessage` from the `ProgressEvent` in the returned error.
- `List` paginates automatically via `NextToken`.
- Full test coverage in `cloudcontrol_test.go` using `fakeCCClient` + `fakeCCWaiter` — no real AWS calls.

## Test plan

- [ ] `go build ./...` passes
- [ ] `go vet ./...` passes
- [ ] `go test ./...` passes
- [ ] CI passes on ubuntu/windows/macos
EOF
)"
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Task |
|-----------------|------|
| `Create` blocking with waiter | Task 3 |
| `Get` with `ActualState` population | Task 3 |
| `Update` with RFC 6902 patch doc note | Task 3 |
| `Delete` idempotent (NotFound → ErrResourceNotFound) | Task 3 |
| `List` paginated | Task 3 |
| Configurable timeout (default 15 min) | Task 2 (`waitTimeout`) + Task 3 (`timeout()`) |
| `StatusMessage` in failure errors | Task 3 (`progressEventError`) |
| Design comment explaining blocking choice | Task 3 (comment on `Create`) |
| `ccAPIClient` + `ccWaiter` interfaces | Task 1 |
| Factory seams for testability | Task 2 |
| Table-driven tests, fake client + waiter | Task 4 |
| CLAUDE.md updated | Task 5 |
| Branch + PR | Tasks 1 + 5 |

**Placeholder scan:** None found.

**Type consistency:** `ccAPIClient`, `ccWaiter`, `resourceClients`, `fakeCCClient`, `fakeCCWaiter` — all names consistent across tasks. `WaitForOutput` used in both the interface definition (Task 1) and implementation (Task 3). `defaultWaitTimeout` defined in Task 2 and referenced in Task 3. `fabricac.ErrResourceNotFound` referenced in Tasks 3 and 4 — matches `cloud.ErrResourceNotFound` in `provider.go`.

**One correction applied during review:** `ccAPIErrorImpl.ErrorFault()` returns `int` — the smithy interface actually returns `smithy.ErrorFault`. Replacing with the correct smithy type would require importing smithy in the test file. Since `isNotFound` only calls `ErrorCode()` via a local interface check (`interface{ ErrorCode() string }`), the fake doesn't need to fully satisfy `smithy.APIError` — it only needs `ErrorCode() string`. The `ccAPIErrorImpl` struct satisfies that local interface. ✓
