package testutil

import (
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

func TestFakeProviderName(t *testing.T) {
	f := &TestProvider{}
	if got := f.Name(); got != "fake" {
		t.Errorf("Name() = %q, want %q", got, "fake")
	}
}

func TestFakeProviderIdentitySuccess(t *testing.T) {
	f := &TestProvider{}
	account, arn, region, err := f.Identity(context.Background())
	if err != nil {
		t.Fatalf("Identity() unexpected error: %v", err)
	}
	if account != "123456789012" {
		t.Errorf("account = %q, want %q", account, "123456789012")
	}
	if region != "us-east-1" {
		t.Errorf("region = %q, want %q", region, "us-east-1")
	}
	if arn == "" {
		t.Error("arn should not be empty")
	}
}

func TestFakeProviderIdentityError(t *testing.T) {
	expectedErr := errors.New("identity unavailable")
	f := &TestProvider{IdentityErr: expectedErr}
	_, _, _, err := f.Identity(context.Background())
	if err != expectedErr {
		t.Errorf("Identity() error = %v, want %v", err, expectedErr)
	}
}

func TestFakeProviderCustomAccountAndRegion(t *testing.T) {
	f := &TestProvider{AccountID: "999999999999", Region: "eu-west-1"}
	account, _, region, err := f.Identity(context.Background())
	if err != nil {
		t.Fatalf("Identity() error: %v", err)
	}
	if account != "999999999999" {
		t.Errorf("account = %q, want %q", account, "999999999999")
	}
	if region != "eu-west-1" {
		t.Errorf("region = %q, want %q", region, "eu-west-1")
	}
}

func TestFakeResourceClientCreateCalls(t *testing.T) {
	f := &TestProvider{}
	rc := f.Resources()

	res := &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup}
	if err := rc.Create(context.Background(), res); err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if f.CreateCalls != 1 {
		t.Errorf("CreateCalls = %d, want 1", f.CreateCalls)
	}
	if len(f.CreatedTypes) != 1 || f.CreatedTypes[0] != cloud.TypeAWSEC2SecurityGroup {
		t.Errorf("CreatedTypes = %v, want [%s]", f.CreatedTypes, cloud.TypeAWSEC2SecurityGroup)
	}
}

func TestFakeResourceClientCreateIdentifiers(t *testing.T) {
	f := &TestProvider{}
	rc := f.Resources()

	for _, tc := range []struct {
		typeName string
		prefix   string
	}{
		{cloud.TypeAWSEC2SecurityGroup, "sg-fake"},
		{cloud.TypeAWSEC2Instance, "i-fake"},
		{cloud.TypeAWSIAMRole, "role-fake"},
		{cloud.TypeAWSIAMInstanceProfile, "profile-fake"},
		{"AWS::S3::Bucket", "fake-AWS::S3::Bucket"},
	} {
		res := &cloud.Resource{TypeName: tc.typeName}
		if err := rc.Create(context.Background(), res); err != nil {
			t.Fatalf("Create(%s) error: %v", tc.typeName, err)
		}
		if len(res.Identifier) == 0 {
			t.Errorf("Identifier should not be empty for %s", tc.typeName)
		}
	}
}

func TestFakeResourceClientCreateError(t *testing.T) {
	expectedErr := errors.New("quota exceeded")
	f := &TestProvider{
		CreateErr: map[string]error{
			cloud.TypeAWSEC2Instance: expectedErr,
		},
	}
	rc := f.Resources()

	// SG should succeed
	sg := &cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup}
	if err := rc.Create(context.Background(), sg); err != nil {
		t.Fatalf("SG Create should succeed: %v", err)
	}

	// Instance should fail
	inst := &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance}
	if err := rc.Create(context.Background(), inst); err != expectedErr {
		t.Errorf("Instance Create error = %v, want %v", err, expectedErr)
	}
}

func TestFakeResourceClientGetNil(t *testing.T) {
	f := &TestProvider{}
	rc := f.Resources()
	err := rc.Get(context.Background(), nil)
	if err != cloud.ErrResourceNotFound {
		t.Errorf("Get(nil) error = %v, want ErrResourceNotFound", err)
	}
}

func TestFakeResourceClientGetWithStored(t *testing.T) {
	f := &TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2Instance: {Identifier: "i-stored"},
		},
	}
	rc := f.Resources()
	res := &cloud.Resource{TypeName: cloud.TypeAWSEC2Instance}
	if err := rc.Get(context.Background(), res); err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if res.Identifier != "i-stored" {
		t.Errorf("Identifier = %q, want %q", res.Identifier, "i-stored")
	}
}

func TestFakeResourceClientDelete(t *testing.T) {
	f := &TestProvider{}
	rc := f.Resources()
	if err := rc.Delete(context.Background(), &cloud.Resource{}); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	if f.DeleteCalls != 1 {
		t.Errorf("DeleteCalls = %d, want 1", f.DeleteCalls)
	}
}

func TestFakeResourceClientUpdate(t *testing.T) {
	f := &TestProvider{}
	rc := f.Resources()
	if err := rc.Update(context.Background(), &cloud.Resource{}); err != nil {
		t.Fatalf("Update error: %v", err)
	}
	if f.UpdateCalls != 1 {
		t.Errorf("UpdateCalls = %d, want 1", f.UpdateCalls)
	}
}

func TestFakeResourceClientListResult(t *testing.T) {
	expected := []cloud.Resource{{TypeName: "test", Identifier: "id1"}}
	f := &TestProvider{ListResult: expected}
	rc := f.Resources()
	result, err := rc.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(result) != 1 || result[0].Identifier != "id1" {
		t.Errorf("List result = %+v, want %+v", result, expected)
	}
}

func TestFakeResourceClientListError(t *testing.T) {
	expectedErr := errors.New("list failed")
	f := &TestProvider{ListErr: expectedErr}
	rc := f.Resources()
	_, err := rc.List(context.Background(), "")
	if err != expectedErr {
		t.Errorf("List error = %v, want %v", err, expectedErr)
	}
}

func TestNewTestState(t *testing.T) {
	st := NewTestState()
	m := st.GetModule("nonexistent")
	if m != nil {
		t.Error("new state should not have modules")
	}
}

func TestNewTestStateWithAccount(t *testing.T) {
	st := NewTestStateWith("111111111111", "ap-southeast-1")
	m := st.GetModule("nonexistent")
	if m != nil {
		t.Error("new state should not have modules")
	}
}

func TestStateWriteCapture(t *testing.T) {
	capture := &StateWriteCapture{}
	writeFunc := capture.WriteFunc()

	st := NewTestState()
	if err := writeFunc(st); err != nil {
		t.Fatalf("write error: %v", err)
	}
	if !capture.Written() {
		t.Error("should report written")
	}
	if capture.Last() == nil {
		t.Error("Last() should not be nil")
	}
}

func TestStateWriteCaptureEmpty(t *testing.T) {
	capture := &StateWriteCapture{}
	if capture.Written() {
		t.Error("should not report written when empty")
	}
	if capture.Last() != nil {
		t.Error("Last() should be nil when empty")
	}
}

func TestStateWriteError(t *testing.T) {
	writeFunc := StateWriteError(2)
	// First write should succeed
	if err := writeFunc(nil); err != nil {
		t.Errorf("first write should succeed: %v", err)
	}
	// Second write should fail
	if err := writeFunc(nil); err == nil {
		t.Error("second write should fail")
	}
}

func TestStateWriteAlwaysError(t *testing.T) {
	writeFunc := StateWriteAlwaysError()
	if err := writeFunc(nil); err == nil {
		t.Error("should always fail")
	}
}

func TestStateWriteNever(t *testing.T) {
	writeFunc := StateWriteNever()
	if err := writeFunc(nil); err != nil {
		t.Errorf("should always succeed: %v", err)
	}
}
