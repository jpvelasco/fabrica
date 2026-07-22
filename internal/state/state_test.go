package state

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/internal/config"
)

// mockLockClient implements lockClient for testing.
type mockLockClient struct {
	items       map[string]*putOutput
	conditionOK bool
	deleteOK    bool
}

func newMockLockClient() *mockLockClient {
	return &mockLockClient{
		items:       make(map[string]*putOutput),
		conditionOK: true,
		deleteOK:    true,
	}
}

func (m *mockLockClient) putItem(ctx context.Context, in *putInput) (*putOutput, error) {
	if !m.conditionOK {
		return nil, fmt.Errorf("conditional put failed")
	}
	for k := range in.Item {
		m.items[k] = nil
	}
	return &putOutput{}, nil
}

func (m *mockLockClient) deleteItem(ctx context.Context, in *deleteInput) (*deleteOutput, error) {
	if !m.deleteOK {
		return nil, fmt.Errorf("conditional delete failed")
	}
	return &deleteOutput{}, nil
}

func TestLockAcquire(t *testing.T) {
	lc := newMockLockClient()
	ls := NewLockStore("fabrica-state-lock", "us-east-1", lc)

	token, err := ls.Acquire(context.Background(), "my-resource", "test-holder")
	if err != nil {
		t.Fatalf("Acquire returned error: %v", err)
	}
	if token == "" {
		t.Fatal("Acquire returned empty token")
	}
}

func TestLockAcquireConflict(t *testing.T) {
	lc := newMockLockClient()
	lc.conditionOK = false
	ls := NewLockStore("fabrica-state-lock", "us-east-1", lc)

	_, err := ls.Acquire(context.Background(), "my-resource", "test-holder")
	if err == nil {
		t.Fatal("expected error on conflict, got nil")
	}
}

func TestLockRelease(t *testing.T) {
	lc := newMockLockClient()
	ls := NewLockStore("fabrica-state-lock", "us-east-1", lc)

	token, err := ls.Acquire(context.Background(), "my-resource", "test-holder")
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	err = ls.Release(context.Background(), "my-resource", token)
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}
}

func TestLockReleaseBadToken(t *testing.T) {
	lc := newMockLockClient()
	lc.deleteOK = false
	ls := NewLockStore("fabrica-state-lock", "us-east-1", lc)

	_, err := ls.Acquire(context.Background(), "my-resource", "test-holder")
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	err = ls.Release(context.Background(), "my-resource", "wrong-token")
	if err == nil {
		t.Fatal("expected error on bad token release")
	}
}

func TestNewState(t *testing.T) {
	st := NewState("123456789012", "us-east-1")
	if st.Version != "0.1" {
		t.Errorf("Version = %q, want 0.1", st.Version)
	}
	if st.Account != "123456789012" {
		t.Errorf("Account = %q, want 123456789012", st.Account)
	}
	if st.Region != "us-east-1" {
		t.Errorf("Region = %q, want us-east-1", st.Region)
	}
	if len(st.Modules) != 0 {
		t.Error("expected empty modules")
	}
	if len(st.History) != 0 {
		t.Error("expected empty history")
	}
	if !st.Created.IsZero() {
		// good
	} else {
		t.Error("Created is zero")
	}
}

func TestStateUpsertModule(t *testing.T) {
	st := NewState("123", "us-east-1")
	res := []ModuleResource{
		{TypeName: "AWS::S3::Bucket", Identifier: "foo", Properties: map[string]string{"Bucket": "foo"}},
	}
	st.UpsertModule("setup", "0.1", "provisioned", res)

	if len(st.Modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(st.Modules))
	}
	m := st.Modules[0]
	if m.Name != "setup" {
		t.Errorf("module name = %q, want setup", m.Name)
	}
	if m.Version != "0.1" {
		t.Errorf("module version = %q, want 0.1", m.Version)
	}
	if m.Status != "provisioned" {
		t.Errorf("module status = %q, want provisioned", m.Status)
	}
	if len(m.Resources) != 1 {
		t.Errorf("expected 1 resource, got %d", len(m.Resources))
	}
}

func TestStateUpsertModuleUpdate(t *testing.T) {
	st := NewState("123", "us-east-1")
	st.UpsertModule("setup", "0.1", "provisioned", []ModuleResource{})
	st.UpsertModule("setup", "0.2", "degraded", []ModuleResource{
		{TypeName: "AWS::DynamoDB::Table", Identifier: "bar"}})

	if len(st.Modules) != 1 {
		t.Fatalf("expected 1 module after update, got %d", len(st.Modules))
	}
	m := st.Modules[0]
	if m.Version != "0.2" {
		t.Errorf("expected version 0.2, got %q", m.Version)
	}
	if m.Status != "degraded" {
		t.Errorf("expected status degraded, got %q", m.Status)
	}
}

func TestStateAddOp(t *testing.T) {
	st := NewState("123", "us-east-1")
	st.AddOp("setup", "create", 5)

	if len(st.History) != 1 {
		t.Fatalf("expected 1 op, got %d", len(st.History))
	}
	op := st.History[0]
	if op.Module != "setup" {
		t.Errorf("op module = %q, want setup", op.Module)
	}
	if op.Action != "create" {
		t.Errorf("op action = %q, want create", op.Action)
	}
	if op.Count != 5 {
		t.Errorf("op count = %d, want 5", op.Count)
	}
}

func TestStateModuleCount(t *testing.T) {
	st := NewState("123", "us-east-1")
	st.UpsertModule("m1", "v1", "ok", []ModuleResource{
		{TypeName: "A", Identifier: "a1"},
		{TypeName: "B", Identifier: "b1"},
	})
	st.UpsertModule("m2", "v1", "ok", []ModuleResource{
		{TypeName: "C", Identifier: "c1"},
	})

	if st.ModuleCount() != 3 {
		t.Errorf("ModuleCount = %d, want 3", st.ModuleCount())
	}
}

func TestLockID(t *testing.T) {
	id := LockID("us-east-1", "fabrica-state-123")
	if id != "us-east-1/fabrica-state-123" {
		t.Errorf("LockID = %q, want us-east-1/fabrica-state-123", id)
	}
}

func TestResolveBackendNames(t *testing.T) {
	cfg := config.Defaults()

	got := ResolveBackendNames(cfg, "123456789012")
	if got.Bucket != "fabrica-state-123456789012" {
		t.Fatalf("Bucket = %q", got.Bucket)
	}
	if got.Table != config.DefaultStateTable {
		t.Fatalf("Table = %q", got.Table)
	}

	cfg.State.Bucket = "custom-bucket"
	cfg.State.Table = "custom-table"
	got = ResolveBackendNames(cfg, "123456789012")
	if got.Bucket != "custom-bucket" || got.Table != "custom-table" {
		t.Fatalf("custom backend = %+v", got)
	}
}

func TestNewSetupPlanResolvesBackendNames(t *testing.T) {
	cfg := config.Defaults()

	plan := NewSetupPlan(cfg, "123456789012", "us-east-1")
	if plan.Backend.Bucket != "fabrica-state-123456789012" {
		t.Fatalf("Bucket = %q", plan.Backend.Bucket)
	}
	// NewSetupPlan must not mutate the caller's config — it only resolves names
	// into the returned plan.
	if cfg.State.Bucket != "" {
		t.Fatalf("NewSetupPlan mutated cfg.State.Bucket to %q; expected no side effect", cfg.State.Bucket)
	}
	if len(plan.Resources) != 2 {
		t.Fatalf("resource count = %d, want 2", len(plan.Resources))
	}
}

func TestStateString(t *testing.T) {
	st := NewState("123", "us-east-1")
	st.UpsertModule("m1", "v1", "ok", []ModuleResource{
		{TypeName: "A", Identifier: "a1"},
	})

	if st.String() != "1" {
		t.Errorf("State.String() = %q, want 1", st.String())
	}
}

func TestBootstrapResultString(t *testing.T) {
	tests := []struct {
		r    BootstrapResult
		want string
	}{
		{BootstrapResult{Name: "S3 bucket foo", Existed: true}, "  S3 bucket foo already exists — skipping"},
		{BootstrapResult{Name: "S3 bucket bar", Existed: false}, "  created S3 bucket bar"},
	}
	for _, tt := range tests {
		got := tt.r.String()
		if got != tt.want {
			t.Errorf("BootstrapResult.String() = %q, want %q", got, tt.want)
		}
	}
}

func TestGenToken(t *testing.T) {
	t1, err := genToken()
	if err != nil {
		t.Fatalf("genToken: %v", err)
	}
	if len(t1) != 32 {
		t.Errorf("token length = %d, want 32", len(t1))
	}

	t2, err := genToken()
	if err != nil {
		t.Fatalf("genToken: %v", err)
	}
	if t1 == t2 {
		t.Error("two tokens should not be identical")
	}
}

func TestStateTimestamps(t *testing.T) {
	before := time.Now().UTC()
	st := NewState("123", "us-east-1")
	after := time.Now().UTC()

	if st.Created.Before(before) || st.Created.After(after) {
		t.Errorf("Created not within expected range: %v", st.Created)
	}
	if st.Updated.Before(before) || st.Updated.After(after) {
		t.Errorf("Updated not within expected range: %v", st.Updated)
	}
}

// TestGetModuleExisting verifies GetModule returns the module when it exists.
func TestGetModuleExisting(t *testing.T) {
	st := NewState("123", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-p4"},
	})

	m := st.GetModule("perforce")
	if m == nil {
		t.Fatal("GetModule returned nil for existing module")
	}
	if m.Name != "perforce" {
		t.Errorf("module name = %q, want perforce", m.Name)
	}
	if m.Status != "ready" {
		t.Errorf("module status = %q, want ready", m.Status)
	}
}

// TestGetModuleNonExistent verifies GetModule returns nil for missing module.
func TestGetModuleNonExistent(t *testing.T) {
	st := NewState("123", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []ModuleResource{})

	m := st.GetModule("horde")
	if m != nil {
		t.Errorf("GetModule should return nil for non-existent module, got %+v", m)
	}
}

// TestGetModuleEmptyState verifies GetModule returns nil on empty state.
func TestGetModuleEmptyState(t *testing.T) {
	st := NewState("123", "us-east-1")
	m := st.GetModule("anything")
	if m != nil {
		t.Errorf("GetModule should return nil on empty state, got %+v", m)
	}
}

// TestGetModuleResourceExisting verifies GetModuleResource finds the resource.
func TestGetModuleResourceExisting(t *testing.T) {
	st := NewState("123", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-123"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-456"},
	})

	res, ok := st.GetModuleResource("perforce", "AWS::EC2::Instance")
	if !ok {
		t.Fatal("GetModuleResource should return true for existing resource")
	}
	if res.Identifier != "i-456" {
		t.Errorf("resource identifier = %q, want i-456", res.Identifier)
	}
}

// TestGetModuleResourceWrongModule verifies GetModuleResource returns false for wrong module.
func TestGetModuleResourceWrongModule(t *testing.T) {
	st := NewState("123", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-456"},
	})

	_, ok := st.GetModuleResource("horde", "AWS::EC2::Instance")
	if ok {
		t.Error("GetModuleResource should return false for wrong module")
	}
}

// TestGetModuleResourceWrongType verifies GetModuleResource returns false for wrong type.
func TestGetModuleResourceWrongType(t *testing.T) {
	st := NewState("123", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "ready", []ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-456"},
	})

	_, ok := st.GetModuleResource("perforce", "AWS::S3::Bucket")
	if ok {
		t.Error("GetModuleResource should return false for wrong resource type")
	}
}

// TestGetModuleResourceNonExistentModule verifies GetModuleResource returns false for missing module.
func TestGetModuleResourceNonExistentModule(t *testing.T) {
	st := NewState("123", "us-east-1")

	_, ok := st.GetModuleResource("nonexistent", "AWS::EC2::Instance")
	if ok {
		t.Error("GetModuleResource should return false for non-existent module")
	}
}

// TestDefaultBucketWithAccount verifies default bucket name includes account ID.
func TestDefaultBucketWithAccount(t *testing.T) {
	got := DefaultBucket("123456789012")
	want := "fabrica-state-123456789012"
	if got != want {
		t.Errorf("DefaultBucket = %q, want %q", got, want)
	}
}

// TestDefaultBucketEmptyAccount verifies default bucket uses placeholder for empty account.
func TestDefaultBucketEmptyAccount(t *testing.T) {
	got := DefaultBucket("")
	want := "fabrica-state-<account-id>"
	if got != want {
		t.Errorf("DefaultBucket(\"\") = %q, want %q", got, want)
	}
}

// TestResolveBackendNamesNilConfig verifies defaults when config is nil.
func TestResolveBackendNamesNilConfig(t *testing.T) {
	got := ResolveBackendNames(nil, "123456789012")
	if got.Bucket != "fabrica-state-123456789012" {
		t.Errorf("bucket = %q, want fabrica-state-123456789012", got.Bucket)
	}
	if got.Table != config.DefaultStateTable {
		t.Errorf("table = %q, want %s", got.Table, config.DefaultStateTable)
	}
}

// TestResolveBackendNamesPartialConfig verifies only set fields override defaults.
func TestResolveBackendNamesPartialConfig(t *testing.T) {
	cfg := config.Defaults()
	cfg.State.Bucket = "my-bucket"
	// Table is empty — should use default

	got := ResolveBackendNames(cfg, "123456789012")
	if got.Bucket != "my-bucket" {
		t.Errorf("bucket = %q, want my-bucket", got.Bucket)
	}
	if got.Table != config.DefaultStateTable {
		t.Errorf("table = %q, want %s", got.Table, config.DefaultStateTable)
	}
}

// TestLockAcquireTokenFormat verifies the lock token is 32 hex chars.
func TestLockAcquireTokenFormat(t *testing.T) {
	lc := newMockLockClient()
	ls := NewLockStore("fabrica-state-lock", "us-east-1", lc)

	token, err := ls.Acquire(context.Background(), "my-resource", "test-holder")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if len(token) != 32 {
		t.Errorf("token length = %d, want 32", len(token))
	}
	// Verify it's valid hex
	for _, c := range token {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("token contains non-hex char: %c", c)
			break
		}
	}
}

// TestGenTokenUniqueness verifies multiple tokens are unique.
func TestGenTokenUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token, err := genToken()
		if err != nil {
			t.Fatalf("genToken[%d]: %v", i, err)
		}
		if seen[token] {
			t.Errorf("duplicate token at iteration %d: %s", i, token)
		}
		seen[token] = true
	}
}
