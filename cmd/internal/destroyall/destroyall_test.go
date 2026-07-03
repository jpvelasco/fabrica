package destroyall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

func okTeardown(ids ...string) ModuleTeardown {
	return func(context.Context) ([]string, error) { return ids, nil }
}

func failTeardown(msg string) ModuleTeardown {
	return func(context.Context) ([]string, error) { return nil, errors.New(msg) }
}

type fakeBackend struct {
	bucketDeleted, tableDeleted bool
}

func (f *fakeBackend) DeleteStateBucket(_ context.Context, b string) (cloud.StateBackendDeleteResult, error) {
	f.bucketDeleted = true
	return cloud.StateBackendDeleteResult{Identifier: b, Deleted: true}, nil
}
func (f *fakeBackend) DeleteStateLockTable(_ context.Context, t string) (cloud.StateBackendDeleteResult, error) {
	f.tableDeleted = true
	return cloud.StateBackendDeleteResult{Identifier: t, Deleted: true}, nil
}

// errBackend fails on bucket deletion, modelling e.g. a non-empty S3 bucket.
type errBackend struct{ tableDeleted bool }

func (errBackend) DeleteStateBucket(_ context.Context, b string) (cloud.StateBackendDeleteResult, error) {
	return cloud.StateBackendDeleteResult{Identifier: b}, errors.New("bucket not empty")
}
func (e *errBackend) DeleteStateLockTable(_ context.Context, t string) (cloud.StateBackendDeleteResult, error) {
	e.tableDeleted = true
	return cloud.StateBackendDeleteResult{Identifier: t, Deleted: true}, nil
}

func baseEngine(out *bytes.Buffer, be cloud.StateBackendDestroyer, mods []Module) Engine {
	return Engine{
		Account:   "123456789012",
		Region:    "us-east-1",
		Bucket:    "fabrica-state-123456789012",
		Table:     "fabrica-state-lock",
		Modules:   mods,
		Backend:   be,
		Out:       out,
		AssumeYes: true, // skip interactive confirm in most tests
		Confirm:   func(string, string) bool { return true },
	}
}

func TestRunAllSucceedDeletesBackend(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	e := baseEngine(&out, be, []Module{
		{Name: "deploy", Teardown: okTeardown("fleet-1")},
		{Name: "perforce", Teardown: okTeardown("i-1", "sg-1")},
	})
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !be.bucketDeleted || !be.tableDeleted {
		t.Fatal("backend should be deleted when all modules succeed")
	}
}

func TestRunBackendDeleteError(t *testing.T) {
	var out bytes.Buffer
	be := &errBackend{}
	e := baseEngine(&out, nil, []Module{{Name: "perforce", Teardown: okTeardown("i-1")}})
	e.Backend = be
	err := e.Run(context.Background())
	if err == nil {
		t.Fatal("expected an error when backend deletion fails")
	}
	if !strings.Contains(err.Error(), "bucket") {
		t.Fatalf("error should mention the bucket failure, got: %v", err)
	}
	// bucket delete failed before the table was attempted
	if be.tableDeleted {
		t.Fatal("lock table must not be deleted after bucket deletion fails")
	}
}

func TestRunModuleFailureSkipsBackend(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	e := baseEngine(&out, be, []Module{
		{Name: "deploy", Teardown: failTeardown("fleet stuck")},
		{Name: "perforce", Teardown: okTeardown("i-1")},
	})
	err := e.Run(context.Background())
	if err == nil {
		t.Fatal("expected an error when a module fails")
	}
	if be.bucketDeleted || be.tableDeleted {
		t.Fatal("backend MUST NOT be deleted when any module fails")
	}
	// remaining module still attempted
	if !strings.Contains(out.String(), "perforce") {
		t.Fatalf("expected perforce still torn down after deploy failure:\n%s", out.String())
	}
	// the failed module is named explicitly in the summary, with its error
	if !strings.Contains(out.String(), "deploy") || !strings.Contains(out.String(), "fleet stuck") {
		t.Fatalf("failure summary must name the failed module and its error:\n%s", out.String())
	}
	// the returned error also lists the failed module
	if !strings.Contains(err.Error(), "deploy") {
		t.Fatalf("returned error must name the failed module, got: %v", err)
	}
}

func TestRunExecutesInGivenOrder(t *testing.T) {
	var out bytes.Buffer
	var order []string
	track := func(name string) ModuleTeardown {
		return func(context.Context) ([]string, error) { order = append(order, name); return nil, nil }
	}
	e := baseEngine(&out, &fakeBackend{}, []Module{
		{Name: "deploy", Teardown: track("deploy")},
		{Name: "ci", Teardown: track("ci")},
		{Name: "perforce", Teardown: track("perforce")},
	})
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"deploy", "ci", "perforce"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("teardown order = %v, want %v", order, want)
	}
}

func TestRunDryRunNoDeletes(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	called := false
	e := baseEngine(&out, be, []Module{
		{Name: "deploy", Teardown: func(context.Context) ([]string, error) { called = true; return nil, nil }},
	})
	e.DryRun = true
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Fatal("dry-run must not invoke module teardown")
	}
	if be.bucketDeleted || be.tableDeleted {
		t.Fatal("dry-run must not delete backend")
	}
	if !strings.Contains(out.String(), "deploy") {
		t.Fatalf("dry-run should list modules:\n%s", out.String())
	}
}

func TestRunConfirmRejected(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	called := false
	e := baseEngine(&out, be, []Module{
		{Name: "deploy", Teardown: func(context.Context) ([]string, error) { called = true; return nil, nil }},
	})
	e.AssumeYes = false
	e.Confirm = func(string, string) bool { return false }
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called || be.bucketDeleted {
		t.Fatal("rejected confirmation must make no changes")
	}
	if !strings.Contains(out.String(), "Cancelled") {
		t.Fatalf("expected cancellation message:\n%s", out.String())
	}
}

func TestRunConfirmPhraseIsAggregate(t *testing.T) {
	var out bytes.Buffer
	var gotPhrase string
	e := baseEngine(&out, &fakeBackend{}, []Module{{Name: "deploy", Teardown: okTeardown()}})
	e.AssumeYes = false
	e.Confirm = func(_ string, phrase string) bool { gotPhrase = phrase; return true }
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotPhrase != "destroy all 123456789012" {
		t.Fatalf("phrase = %q, want %q", gotPhrase, "destroy all 123456789012")
	}
}

func TestRunEmptyNoModulesNoBackend(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	e := baseEngine(&out, be, nil)
	e.Bucket = ""
	e.Table = ""
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if be.bucketDeleted {
		t.Fatal("nothing to delete")
	}
}

func TestRunJSONOutput(t *testing.T) {
	var out bytes.Buffer
	e := baseEngine(&out, &fakeBackend{}, []Module{{Name: "deploy", Teardown: okTeardown("fleet-1")}})
	e.JSONOut = true
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var res Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if len(res.Modules) != 1 || res.Modules[0].Module != "deploy" || !res.BackendDeleted {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// TestRunDryRunJSONOutput verifies --dry-run with JSON output format.
func TestRunDryRunJSONOutput(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	e := baseEngine(&out, be, []Module{
		{Name: "deploy", Teardown: okTeardown("fleet-1")},
		{Name: "perforce", Teardown: okTeardown("i-1")},
	})
	e.DryRun = true
	e.JSONOut = true
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var res Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if !res.DryRun {
		t.Error("DryRun must be true")
	}
	if len(res.Modules) != 2 {
		t.Errorf("expected 2 modules in dry-run output, got %d", len(res.Modules))
	}
	if be.bucketDeleted || be.tableDeleted {
		t.Fatal("dry-run must not delete backend")
	}
}

// TestRunModuleFailureJSONOutput verifies JSON output when a module fails.
func TestRunModuleFailureJSONOutput(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	e := baseEngine(&out, be, []Module{
		{Name: "deploy", Teardown: failTeardown("fleet stuck")},
		{Name: "perforce", Teardown: okTeardown("i-1")},
	})
	e.JSONOut = true
	err := e.Run(context.Background())
	if err == nil {
		t.Fatal("expected an error when a module fails")
	}
	var res Result
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &res); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out.String())
		}
		if res.Modules[0].Error == "" {
			t.Error("deploy module result should contain error")
		}
	}
}

// TestBackendDeleteMissingBucket verifies printBackendResult with Missing=true.
func TestBackendDeleteMissingBucket(t *testing.T) {
	var out bytes.Buffer
	be := &missingBackend{}
	e := baseEngine(&out, be, []Module{{Name: "perforce", Teardown: okTeardown("i-1")}})
	e.JSONOut = false
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "not found") {
		t.Fatalf("expected 'not found' message for missing backend:\n%s", out.String())
	}
}

// TestBackendDeleteUnchanged verifies printBackendResult with Deleted=false, Missing=false.
func TestBackendDeleteUnchanged(t *testing.T) {
	var out bytes.Buffer
	be := &unchangedBackend{}
	e := baseEngine(&out, be, []Module{{Name: "perforce", Teardown: okTeardown("i-1")}})
	e.JSONOut = false
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "unchanged") {
		t.Fatalf("expected 'unchanged' message for unchanged backend:\n%s", out.String())
	}
}

// missingBackend returns Missing=true when asked to delete.
type missingBackend struct{ tableDeleted bool }

func (missingBackend) DeleteStateBucket(_ context.Context, b string) (cloud.StateBackendDeleteResult, error) {
	return cloud.StateBackendDeleteResult{Identifier: b, Missing: true}, nil
}
func (m *missingBackend) DeleteStateLockTable(_ context.Context, t string) (cloud.StateBackendDeleteResult, error) {
	m.tableDeleted = true
	return cloud.StateBackendDeleteResult{Identifier: t, Deleted: true}, nil
}

// unchangedBackend returns Deleted=false, Missing=false.
type unchangedBackend struct{}

func (unchangedBackend) DeleteStateBucket(_ context.Context, b string) (cloud.StateBackendDeleteResult, error) {
	return cloud.StateBackendDeleteResult{Identifier: b, Deleted: false, Missing: false}, nil
}
func (unchangedBackend) DeleteStateLockTable(_ context.Context, t string) (cloud.StateBackendDeleteResult, error) {
	return cloud.StateBackendDeleteResult{Identifier: t, Deleted: false, Missing: false}, nil
}

// TestRunNoBackendNames verifies engine works when Bucket and Table are empty.
func TestRunNoBackendNames(t *testing.T) {
	var out bytes.Buffer
	be := &fakeBackend{}
	e := baseEngine(&out, be, []Module{{Name: "perforce", Teardown: okTeardown("i-1")}})
	e.Bucket = ""
	e.Table = ""
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run with no backend: %v", err)
	}
	if be.bucketDeleted || be.tableDeleted {
		t.Fatal("should not delete backend when names are empty")
	}
}

// TestRunTableDeleteError verifies backend error on table deletion.
func TestRunTableDeleteError(t *testing.T) {
	var out bytes.Buffer
	be := &tableErrorBackend{}
	e := baseEngine(&out, be, []Module{{Name: "perforce", Teardown: okTeardown("i-1")}})
	err := e.Run(context.Background())
	if err == nil {
		t.Fatal("expected an error when table deletion fails")
	}
	if !strings.Contains(err.Error(), "table") {
		t.Fatalf("error should mention table failure, got: %v", err)
	}
}

// tableErrorBackend fails on table deletion only.
type tableErrorBackend struct{}

func (tableErrorBackend) DeleteStateBucket(_ context.Context, b string) (cloud.StateBackendDeleteResult, error) {
	return cloud.StateBackendDeleteResult{Identifier: b, Deleted: true}, nil
}
func (tableErrorBackend) DeleteStateLockTable(_ context.Context, t string) (cloud.StateBackendDeleteResult, error) {
	return cloud.StateBackendDeleteResult{Identifier: t}, errors.New("table locked")
}
