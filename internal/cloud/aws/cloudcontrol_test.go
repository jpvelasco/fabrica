package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/oplog"
)

// newCCTestClients returns a resourceClients wired with the provided fakes.
// cc is pre-set so ensureClient is a no-op.
func newCCTestClients(fakeClient *fakeCCClient, fakeWaiter *fakeCCWaiter) *resourceClients {
	return &resourceClients{
		cc:      fakeClient,
		waiter:  fakeWaiter,
		version: "test",
	}
}

// newCCTestClientsWithRegion is like newCCTestClients but sets awsCfg.region
// so that oplog lines include the region attribute.
func newCCTestClientsWithRegion(fakeClient *fakeCCClient, fakeWaiter *fakeCCWaiter, region string) *resourceClients {
	return &resourceClients{
		cc:      fakeClient,
		waiter:  fakeWaiter,
		awsCfg:  awsConfig{region: region},
		version: "test",
	}
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
	assertStringContains(t, err.Error(), "creating AWS::EC2::SecurityGroup")
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
	assertStringContains(t, err.Error(), "waiting for AWS::EC2::SecurityGroup creation")
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
	assertStringContains(t, err.Error(), "GroupName already exists")
	assertStringContains(t, err.Error(), "InvalidRequest")
}

func TestCreate_AlreadyExists_RecoverIdentifier(t *testing.T) {
	existingID := "sg-already-exists"
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
		},
	}
	waiter := &fakeCCWaiter{
		out: &cloudcontrol.GetResourceRequestStatusOutput{
			ProgressEvent: &types.ProgressEvent{
				OperationStatus: types.OperationStatusFailed,
				ErrorCode:       types.HandlerErrorCodeAlreadyExists,
				Identifier:      awssdk.String(existingID),
				StatusMessage:   awssdk.String("Resource already exists"),
			},
		},
	}
	rc := newCCTestClients(client, waiter)

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", DesiredState: json.RawMessage(`{}`)}
	err := rc.Create(context.Background(), r)
	if err != nil {
		t.Fatalf("Create should not error on AlreadyExists: %v", err)
	}
	if r.Identifier != existingID {
		t.Errorf("Identifier = %q, want %q (recovered from existing resource)", r.Identifier, existingID)
	}
}

func TestCreate_AlreadyExists_EmptyIdentifier(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
		},
	}
	waiter := &fakeCCWaiter{
		out: &cloudcontrol.GetResourceRequestStatusOutput{
			ProgressEvent: &types.ProgressEvent{
				OperationStatus: types.OperationStatusFailed,
				ErrorCode:       types.HandlerErrorCodeAlreadyExists,
				StatusMessage:   awssdk.String("Resource already exists"),
			},
		},
	}
	rc := newCCTestClients(client, waiter)

	r := &fabricac.Resource{TypeName: "AWS::IAM::Role", DesiredState: json.RawMessage(`{}`)}
	err := rc.Create(context.Background(), r)
	if err == nil {
		t.Fatal("Create should error on AlreadyExists without identifier")
	}
	assertStringContains(t, err.Error(), "Resource already exists")
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
	assertStringContains(t, err.Error(), "getting AWS::EC2::SecurityGroup sg-abc123")
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
	assertStringContains(t, err.Error(), "waiting for AWS::EC2::SecurityGroup update")
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
	assertStringContains(t, err.Error(), "waiting for AWS::EC2::SecurityGroup deletion")
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
	assertStringContains(t, err.Error(), "dependency violation")
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
	assertStringContains(t, err.Error(), "listing AWS::EC2::SecurityGroup")
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
	assertStringContains(t, err.Error(), "loading AWS config for Cloud Control")
}

// --- timeout ---

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

// --- oplog error-path coverage ---

func TestCreate_SDKError_LogsError(t *testing.T) {
	var buf bytes.Buffer
	oplog.ResetForTest()
	oplog.InitWithWriter(0, true, &buf) // verbose=true forces debug level

	client := &fakeCCClient{createErr: fmt.Errorf("access denied")}
	rc := newCCTestClientsWithRegion(client, &fakeCCWaiter{}, "us-east-1")

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", DesiredState: json.RawMessage(`{}`)}
	err := rc.Create(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertStringContains(t, err.Error(), "creating AWS::EC2::SecurityGroup")

	out := buf.String()
	if !strings.Contains(out, "cloud control create failed") {
		t.Fatalf("log missing 'cloud control create failed': %s", out)
	}
	if !strings.Contains(out, "us-east-1") {
		t.Fatalf("log missing region 'us-east-1': %s", out)
	}
}

func TestCreate_AlreadyExists_LogsDebug(t *testing.T) {
	var buf bytes.Buffer
	oplog.ResetForTest()
	oplog.InitWithWriter(0, true, &buf) // verbose=true forces debug level

	existingID := "sg-already-exists"
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{RequestToken: awssdk.String("tok")},
		},
	}
	waiter := &fakeCCWaiter{
		out: &cloudcontrol.GetResourceRequestStatusOutput{
			ProgressEvent: &types.ProgressEvent{
				OperationStatus: types.OperationStatusFailed,
				ErrorCode:       types.HandlerErrorCodeAlreadyExists,
				Identifier:      awssdk.String(existingID),
				StatusMessage:   awssdk.String("Resource already exists"),
			},
		},
	}
	rc := newCCTestClientsWithRegion(client, waiter, "eu-west-1")

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", DesiredState: json.RawMessage(`{}`)}
	err := rc.Create(context.Background(), r)
	if err != nil {
		t.Fatalf("Create should not error on AlreadyExists: %v", err)
	}
	if r.Identifier != existingID {
		t.Errorf("Identifier = %q, want %q", r.Identifier, existingID)
	}

	out := buf.String()
	if !strings.Contains(out, "resource already exists") {
		t.Fatalf("log missing 'resource already exists': %s", out)
	}
	if !strings.Contains(out, "eu-west-1") {
		t.Fatalf("log missing region 'eu-west-1': %s", out)
	}
}

func TestGet_NotFound_LogsDebug(t *testing.T) {
	var buf bytes.Buffer
	oplog.ResetForTest()
	oplog.InitWithWriter(0, true, &buf) // verbose=true forces debug level

	client := &fakeCCClient{getErr: ccAPIError("NotFound")}
	rc := newCCTestClientsWithRegion(client, nil, "ap-southeast-1")

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-missing"}
	err := rc.Get(context.Background(), r)
	if !errors.Is(err, fabricac.ErrResourceNotFound) {
		t.Fatalf("error = %v, want ErrResourceNotFound", err)
	}

	out := buf.String()
	if !strings.Contains(out, "not found") {
		t.Fatalf("log missing 'not found': %s", out)
	}
	if !strings.Contains(out, "ap-southeast-1") {
		t.Fatalf("log missing region 'ap-southeast-1': %s", out)
	}
}

func TestGet_SDKError_LogsError(t *testing.T) {
	var buf bytes.Buffer
	oplog.ResetForTest()
	oplog.InitWithWriter(0, true, &buf) // verbose=true forces debug level

	client := &fakeCCClient{getErr: fmt.Errorf("network error")}
	rc := newCCTestClientsWithRegion(client, nil, "us-west-2")

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc123"}
	err := rc.Get(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertStringContains(t, err.Error(), "getting AWS::EC2::SecurityGroup sg-abc123")

	out := buf.String()
	if !strings.Contains(out, "cloud control get failed") {
		t.Fatalf("log missing 'cloud control get failed': %s", out)
	}
	if !strings.Contains(out, "us-west-2") {
		t.Fatalf("log missing region 'us-west-2': %s", out)
	}
}

func TestUpdate_SDKError_LogsError(t *testing.T) {
	var buf bytes.Buffer
	oplog.ResetForTest()
	oplog.InitWithWriter(0, true, &buf) // verbose=true forces debug level

	client := &fakeCCClient{updateErr: fmt.Errorf("throttling")}
	rc := newCCTestClientsWithRegion(client, &fakeCCWaiter{}, "us-east-1")

	r := &fabricac.Resource{
		TypeName:     "AWS::EC2::SecurityGroup",
		Identifier:   "sg-abc123",
		DesiredState: json.RawMessage(`[]`),
	}
	err := rc.Update(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertStringContains(t, err.Error(), "updating AWS::EC2::SecurityGroup sg-abc123")

	out := buf.String()
	if !strings.Contains(out, "cloud control update failed") {
		t.Fatalf("log missing 'cloud control update failed': %s", out)
	}
	if !strings.Contains(out, "us-east-1") {
		t.Fatalf("log missing region 'us-east-1': %s", out)
	}
}

func TestDelete_NotFound_LogsDebug(t *testing.T) {
	var buf bytes.Buffer
	oplog.ResetForTest()
	oplog.InitWithWriter(0, true, &buf) // verbose=true forces debug level

	client := &fakeCCClient{deleteErr: ccAPIError("NotFound")}
	rc := newCCTestClientsWithRegion(client, nil, "us-east-1")

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-missing"}
	err := rc.Delete(context.Background(), r)
	if !errors.Is(err, fabricac.ErrResourceNotFound) {
		t.Fatalf("error = %v, want ErrResourceNotFound", err)
	}

	out := buf.String()
	if !strings.Contains(out, "already deleted") {
		t.Fatalf("log missing 'already deleted': %s", out)
	}
	if !strings.Contains(out, "us-east-1") {
		t.Fatalf("log missing region 'us-east-1': %s", out)
	}
}

func TestDelete_SDKError_LogsError(t *testing.T) {
	var buf bytes.Buffer
	oplog.ResetForTest()
	oplog.InitWithWriter(0, true, &buf) // verbose=true forces debug level

	client := &fakeCCClient{deleteErr: fmt.Errorf("dependency violation")}
	rc := newCCTestClientsWithRegion(client, nil, "eu-central-1")

	r := &fabricac.Resource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc123"}
	err := rc.Delete(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertStringContains(t, err.Error(), "deleting AWS::EC2::SecurityGroup sg-abc123")

	out := buf.String()
	if !strings.Contains(out, "cloud control delete failed") {
		t.Fatalf("log missing 'cloud control delete failed': %s", out)
	}
	if !strings.Contains(out, "eu-central-1") {
		t.Fatalf("log missing region 'eu-central-1': %s", out)
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
	listPages [][]types.ResourceDescription
	listErr   error

	// statusOuts simulates GetResourceRequestStatus responses for polling paths.
	statusOuts []*cloudcontrol.GetResourceRequestStatusOutput
	statusErr  error
}

func (f *fakeCCClient) CreateResource(_ context.Context, _ *cloudcontrol.CreateResourceInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.CreateResourceOutput, error) {
	f.createCalls++
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createOut, nil
}

func (f *fakeCCClient) GetResource(_ context.Context, _ *cloudcontrol.GetResourceInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error) {
	f.getCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getOut, nil
}

func (f *fakeCCClient) UpdateResource(_ context.Context, _ *cloudcontrol.UpdateResourceInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.UpdateResourceOutput, error) {
	f.updateCalls++
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return f.updateOut, nil
}

func (f *fakeCCClient) DeleteResource(_ context.Context, _ *cloudcontrol.DeleteResourceInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.DeleteResourceOutput, error) {
	f.deleteCalls++
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return f.deleteOut, nil
}

func (f *fakeCCClient) ListResources(_ context.Context, _ *cloudcontrol.ListResourcesInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.ListResourcesOutput, error) {
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

func (f *fakeCCClient) GetResourceRequestStatus(_ context.Context, _ *cloudcontrol.GetResourceRequestStatusInput, _ ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceRequestStatusOutput, error) {
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	if len(f.statusOuts) > 0 {
		out := f.statusOuts[0]
		f.statusOuts = f.statusOuts[1:]
		return out, nil
	}
	return &cloudcontrol.GetResourceRequestStatusOutput{}, nil
}

type fakeCCWaiter struct {
	calls int
	token string
	out   *cloudcontrol.GetResourceRequestStatusOutput
	err   error
}

func (f *fakeCCWaiter) WaitForOutput(_ context.Context, in *cloudcontrol.GetResourceRequestStatusInput, _ time.Duration, _ ...func(*cloudcontrol.ResourceRequestSuccessWaiterOptions)) (*cloudcontrol.GetResourceRequestStatusOutput, error) {
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

// ccAPIErrorImpl satisfies the local interface{ ErrorCode() string } used by isNotFound.
type ccAPIErrorImpl struct{ code string }

func (e *ccAPIErrorImpl) ErrorCode() string { return e.code }
func (e *ccAPIErrorImpl) Error() string     { return e.code }

func ccAPIError(code string) error { return &ccAPIErrorImpl{code: code} }

// --- createAsync ---

func TestCreateAsync_ImmediateIdentifier(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{
				Identifier: awssdk.String("fleet-123"),
			},
		},
	}
	rc := newCCTestClients(client, nil)

	r := &fabricac.Resource{
		TypeName:     "AWS::GameLift::Fleet",
		DesiredState: json.RawMessage(`{"Name":"test-fleet"}`),
	}
	if err := rc.createAsync(context.Background(), r); err != nil {
		t.Fatalf("createAsync: %v", err)
	}
	if r.Identifier != "fleet-123" {
		t.Errorf("Identifier = %q, want fleet-123", r.Identifier)
	}
}

func TestCreateAsync_PollThenIdentifier(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{
				RequestToken: awssdk.String("tok-async"),
			},
		},
		statusOuts: []*cloudcontrol.GetResourceRequestStatusOutput{
			{ProgressEvent: &types.ProgressEvent{OperationStatus: types.OperationStatusInProgress}},
			{ProgressEvent: &types.ProgressEvent{OperationStatus: types.OperationStatusInProgress, Identifier: awssdk.String("fleet-456")}},
		},
	}
	rc := newCCTestClients(client, nil)

	r := &fabricac.Resource{
		TypeName:     "AWS::GameLift::Fleet",
		DesiredState: json.RawMessage(`{"Name":"test-fleet"}`),
	}
	if err := rc.createAsync(context.Background(), r); err != nil {
		t.Fatalf("createAsync: %v", err)
	}
	if r.Identifier != "fleet-456" {
		t.Errorf("Identifier = %q, want fleet-456", r.Identifier)
	}
}

func TestCreateAsync_SDKError(t *testing.T) {
	client := &fakeCCClient{createErr: fmt.Errorf("access denied")}
	rc := newCCTestClients(client, nil)

	r := &fabricac.Resource{TypeName: "AWS::GameLift::Fleet", DesiredState: json.RawMessage(`{}`)}
	err := rc.createAsync(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertStringContains(t, err.Error(), "creating AWS::GameLift::Fleet")
}

func TestCreateAsync_PollFailedStatus(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{
				RequestToken: awssdk.String("tok-async-fail"),
			},
		},
		statusOuts: []*cloudcontrol.GetResourceRequestStatusOutput{
			{ProgressEvent: &types.ProgressEvent{
				OperationStatus: types.OperationStatusFailed,
				ErrorCode:       types.HandlerErrorCodeGeneralServiceException,
				StatusMessage:   awssdk.String("bad thing"),
			}},
		},
	}
	rc := newCCTestClients(client, nil)

	r := &fabricac.Resource{
		TypeName:     "AWS::GameLift::Fleet",
		DesiredState: json.RawMessage(`{"Name":"test-fleet"}`),
	}
	err := rc.createAsync(context.Background(), r)
	if err == nil {
		t.Fatal("expected error")
	}
	assertStringContains(t, err.Error(), "bad thing")
}

func TestCreateAsync_AlreadyExists_RecoverIdentifier(t *testing.T) {
	existingID := "fleet-already-exists"
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{
				RequestToken: awssdk.String("tok-async-aex"),
			},
		},
		statusOuts: []*cloudcontrol.GetResourceRequestStatusOutput{
			{ProgressEvent: &types.ProgressEvent{
				OperationStatus: types.OperationStatusFailed,
				ErrorCode:       types.HandlerErrorCodeAlreadyExists,
				Identifier:      awssdk.String(existingID),
				StatusMessage:   awssdk.String("Resource already exists"),
			}},
		},
	}
	rc := newCCTestClients(client, nil)

	r := &fabricac.Resource{
		TypeName:     "AWS::GameLift::Fleet",
		DesiredState: json.RawMessage(`{"Name":"test-fleet"}`),
	}
	err := rc.createAsync(context.Background(), r)
	if err != nil {
		t.Fatalf("createAsync should not error on AlreadyExists: %v", err)
	}
	if r.Identifier != existingID {
		t.Errorf("Identifier = %q, want %q (recovered from existing resource)", r.Identifier, existingID)
	}
}

func TestCreateAsync_AlreadyExists_EmptyIdentifier(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{
				RequestToken: awssdk.String("tok-async-aex-empty"),
			},
		},
		statusOuts: []*cloudcontrol.GetResourceRequestStatusOutput{
			{ProgressEvent: &types.ProgressEvent{
				OperationStatus: types.OperationStatusFailed,
				ErrorCode:       types.HandlerErrorCodeAlreadyExists,
				StatusMessage:   awssdk.String("Resource already exists"),
			}},
		},
	}
	rc := newCCTestClients(client, nil)

	r := &fabricac.Resource{
		TypeName:     "AWS::GameLift::Fleet",
		DesiredState: json.RawMessage(`{"Name":"test-fleet"}`),
	}
	err := rc.createAsync(context.Background(), r)
	if err == nil {
		t.Fatal("createAsync should error on AlreadyExists without identifier")
	}
	assertStringContains(t, err.Error(), "already exists")
}

func TestCreateAsync_TagsInjected(t *testing.T) {
	client := &fakeCCClient{
		createOut: &cloudcontrol.CreateResourceOutput{
			ProgressEvent: &types.ProgressEvent{
				Identifier: awssdk.String("fleet-789"),
			},
		},
	}
	rc := newCCTestClients(client, nil)

	r := &fabricac.Resource{
		TypeName:     "AWS::GameLift::Fleet",
		DesiredState: json.RawMessage(`{}`),
	}
	if err := rc.createAsync(context.Background(), r); err != nil {
		t.Fatalf("createAsync: %v", err)
	}
	// Tags should have been injected into DesiredState.
	var doc map[string]any
	if err := json.Unmarshal(r.DesiredState, &doc); err != nil {
		t.Fatalf("DesiredState not JSON: %s", r.DesiredState)
	}
	if _, hasTags := doc["Tags"]; !hasTags {
		t.Error("DesiredState missing Tags after createAsync")
	}
}
