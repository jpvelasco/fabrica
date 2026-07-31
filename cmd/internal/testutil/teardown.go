package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// teardownOutput mirrors teardown.Output for JSON parsing in shared tests.
// A local copy avoids an import cycle: testutil ← teardown ← modstatus ← testutil.
type teardownOutput struct {
	Destroyed []string `json:"destroyed"`
	Skipped   []string `json:"skipped,omitempty"`
	DryRun    bool     `json:"dryRun"`
}

// TeardownTestSpec describes one module's teardown test configuration.
type TeardownTestSpec struct {
	// ModuleName is the module identifier (e.g. "horde", "perforce", "workstation").
	ModuleName string
	// Verb is the CLI subcommand verb ("destroy" or "terminate").
	Verb string
	// Version is the module version string used in the state fixture.
	Version string
	// ExpectedDeletes is the number of Delete calls expected on --yes.
	ExpectedDeletes int
	// Resources describes the module's state resources.
	Resources []StateResource
	// SuccessVerb is the word expected in success output ("destroyed" or "terminated").
	SuccessVerb string
	// Status is the module status in the state fixture.
	Status string
	// NewCmd constructs the teardown subcommand.
	NewCmd func(globals.RuntimeSource, globals.OptionsSource, io.Writer) *cobra.Command
	// NewTeardown constructs the orchestrated teardown command.
	// Returns any to avoid importing cmd/internal/teardown directly (import cycle).
	NewTeardown func(globals.Runtime, io.Writer) any
}

// teardownField checks a named exported field on a struct value via reflection.
// This avoids importing cmd/internal/teardown directly, which would create an
// import cycle (testutil ← teardown ← modstatus ← testutil).
func teardownField(t *testing.T, v any, name string) reflect.Value {
	t.Helper()
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
	// Test-only helper; field names are hardcoded in test code, never user-controlled.
	return rv.FieldByName(name)
}

// teardownFieldBool returns a bool field from the teardown command struct.
func teardownFieldBool(t *testing.T, v any, name string) bool {
	t.Helper()
	f := teardownField(t, v, name)
	if !f.IsValid() {
		t.Fatalf("field %s not found", name)
	}
	return f.Bool()
}

// teardownFieldNil checks if a field is nil.
func teardownFieldNil(t *testing.T, v any, name string) bool {
	t.Helper()
	f := teardownField(t, v, name)
	if !f.IsValid() {
		t.Fatalf("field %s not found", name)
	}
	return f.IsNil()
}

// RunTeardownCobraTests runs the standard suite of teardown cobra tests.
// It covers: not provisioned, dry-run (no delete calls, shows resources),
// yes flag (destroys), JSON variants (not provisioned, dry-run, yes),
// nil provider, runtime error, teardown wiring (with provider), and teardown
// nil provider.
//
// Module-specific tests (e.g. DDC resource ordering) should remain in the
// module's own cobra_test.go file.
func RunTeardownCobraTests(t *testing.T, spec TeardownTestSpec) {
	t.Helper()

	if spec.Status == "" {
		spec.Status = "provisioning"
	}
	if spec.SuccessVerb == "" {
		spec.SuccessVerb = "destroyed"
	}

	stateJSON := func() string {
		return NewProvisionedStateJSON(StateModule{
			Name: spec.ModuleName, Version: spec.Version, Status: spec.Status,
			Resources: spec.Resources,
		})
	}

	runCmd := func(t *testing.T, rt globals.RuntimeSource, args ...string) (string, error) {
		t.Helper()
		var out bytes.Buffer
		root, optionsSource := BuildTestSubcommand(&out)
		root.AddCommand(spec.NewCmd(rt, optionsSource, &out))
		return RunCommandWithOut(t, root, &out, append([]string{spec.Verb}, args...)...)
	}

	t.Run("NotProvisioned", func(t *testing.T) {
		t.Chdir(t.TempDir())
		got, err := runCmd(t, NewTestRuntime(&TestProvider{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		AssertContains(t, got, "not provisioned")
	})

	t.Run("DryRunNoDeleteCalls", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		WriteStateFile(t, dir, stateJSON())

		provider := &TestProvider{}
		got, err := runCmd(t, NewTestRuntime(provider), "--dry-run")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		AssertContains(t, got, "dry run")
		if provider.DeleteCalls != 0 {
			t.Errorf("dry-run made %d delete calls, want 0", provider.DeleteCalls)
		}
	})

	t.Run("DryRunShowsResources", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		WriteStateFile(t, dir, stateJSON())

		got, err := runCmd(t, NewTestRuntime(&TestProvider{}), "--dry-run")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Assert that at least the first two resource IDs appear in output.
		if len(spec.Resources) >= 2 {
			AssertContains(t, got, spec.Resources[0].Identifier)
			AssertContains(t, got, spec.Resources[1].Identifier)
		} else if len(spec.Resources) == 1 {
			AssertContains(t, got, spec.Resources[0].Identifier)
		}
	})

	t.Run("YesFlagDestroysResources", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		WriteStateFile(t, dir, stateJSON())

		provider := &TestProvider{}
		got, err := runCmd(t, NewTestRuntime(provider), "--yes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if provider.DeleteCalls != spec.ExpectedDeletes {
			t.Errorf("expected %d delete calls, got %d", spec.ExpectedDeletes, provider.DeleteCalls)
		}
		AssertContains(t, got, spec.SuccessVerb)
	})

	t.Run("JSONNotProvisioned", func(t *testing.T) {
		t.Chdir(t.TempDir())
		got, err := runCmd(t, NewTestRuntime(&TestProvider{}), "--json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var result teardownOutput
		if err := json.Unmarshal([]byte(got), &result); err != nil {
			t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
		}
		if len(result.Destroyed) != 0 {
			t.Errorf("destroyed = %v, want empty", result.Destroyed)
		}
	})

	t.Run("JSONDryRun", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		WriteStateFile(t, dir, stateJSON())

		got, err := runCmd(t, NewTestRuntime(&TestProvider{}), "--json", "--dry-run")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var result teardownOutput
		if err := json.Unmarshal([]byte(got), &result); err != nil {
			t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
		}
		if !result.DryRun {
			t.Error("dryRun must be true")
		}
		if len(result.Destroyed) != spec.ExpectedDeletes {
			t.Errorf("expected %d in destroyed list for dry run, got %d", spec.ExpectedDeletes, len(result.Destroyed))
		}
	})

	t.Run("JSONYes", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		WriteStateFile(t, dir, stateJSON())

		got, err := runCmd(t, NewTestRuntime(&TestProvider{}), "--json", "--yes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var result teardownOutput
		if err := json.Unmarshal([]byte(got), &result); err != nil {
			t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
		}
		if result.DryRun {
			t.Error("dryRun must be false")
		}
		if len(result.Destroyed) != spec.ExpectedDeletes {
			t.Errorf("expected %d destroyed, got %d: %v", spec.ExpectedDeletes, len(result.Destroyed), result.Destroyed)
		}
	})

	t.Run("NilProvider", func(t *testing.T) {
		t.Chdir(t.TempDir())
		got, err := runCmd(t, NewNilProviderRuntime())
		if err != nil {
			t.Fatalf("nil provider: unexpected error: %v", err)
		}
		AssertContains(t, got, "not provisioned")
	})

	t.Run("RuntimeError", func(t *testing.T) {
		src := func() (globals.Runtime, error) {
			return globals.Runtime{}, fmt.Errorf("config not loaded")
		}
		_, err := runCmd(t, src)
		if err == nil {
			t.Fatal("expected error when runtimeSource fails")
		}
	})

	t.Run("TeardownWiring", func(t *testing.T) {
		var out bytes.Buffer
		rt := globals.Runtime{Config: config.Defaults(), Provider: &TestProvider{}}
		tc := spec.NewTeardown(rt, &out)
		if !teardownFieldBool(t, tc, "SkipConfirm") || !teardownFieldBool(t, tc, "AssumeYes") {
			t.Fatalf("SkipConfirm/AssumeYes must be true; got SkipConfirm=%v, AssumeYes=%v",
				teardownFieldBool(t, tc, "SkipConfirm"), teardownFieldBool(t, tc, "AssumeYes"))
		}
		for _, name := range []string{"ReadState", "WriteState", "DeleteResource", "GetResource"} {
			if teardownFieldNil(t, tc, name) {
				t.Fatalf("%s must not be nil when provider is non-nil", name)
			}
		}
		// Check Runtime.Provider is non-nil
		rtField := teardownField(t, tc, "Runtime")
		if !rtField.IsValid() {
			t.Fatal("Runtime field not found")
		}
		provField := rtField.FieldByName("Provider")
		if provField.IsNil() {
			t.Fatal("Runtime.Provider must be set")
		}
		// Check Spec.ModuleName
		specField := teardownField(t, tc, "Spec")
		if !specField.IsValid() {
			t.Fatal("Spec field not found")
		}
		moduleNameField := specField.FieldByName("ModuleName")
		if !moduleNameField.IsValid() {
			t.Fatal("Spec.ModuleName field not found")
		}
		if moduleNameField.String() != spec.ModuleName {
			t.Errorf("module name = %q, want %q", moduleNameField.String(), spec.ModuleName)
		}
	})

	t.Run("TeardownNilProvider", func(t *testing.T) {
		var out bytes.Buffer
		rt := globals.Runtime{Config: config.Defaults(), Provider: nil}
		tc := spec.NewTeardown(rt, &out)
		if !teardownFieldBool(t, tc, "SkipConfirm") || !teardownFieldBool(t, tc, "AssumeYes") {
			t.Fatal("SkipConfirm/AssumeYes must be true even with nil provider")
		}
		for _, name := range []string{"ReadState", "WriteState"} {
			if teardownFieldNil(t, tc, name) {
				t.Fatalf("%s must always be wired", name)
			}
		}
		for _, name := range []string{"DeleteResource", "GetResource"} {
			if !teardownFieldNil(t, tc, name) {
				t.Fatalf("%s must be nil when provider is nil", name)
			}
		}
	})
}
