package testutil

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

// CreateTestSpec describes one module's create test configuration.
type CreateTestSpec struct {
	// ModuleName is the state module key (e.g. "horde", "perforce").
	ModuleName string
	// ExpectedCreates is the number of Create calls expected on --yes.
	ExpectedCreates int
	// ResourceNames are the names expected in dry-run output.
	ResourceNames []string
	// HasInstanceTypeFlag is true if the command supports --instance-type.
	HasInstanceTypeFlag bool
	// InstanceTypeValue is the value to test --instance-type with.
	InstanceTypeValue string
	// HasVolumeSizeFlag is true if the command supports --volume-size.
	HasVolumeSizeFlag bool
	// VolumeSizeValue is the value to test --volume-size with.
	VolumeSizeValue string
	// VolumeSizeOutput is the expected output fragment for --volume-size.
	VolumeSizeOutput string
	// NewCmd constructs the create subcommand.
	NewCmd func(globals.RuntimeSource, globals.OptionsSource, io.Writer) *cobra.Command
	// NewTestRuntime creates a runtime source with a test provider wired.
	// The returned provider is checked for CreateCalls after each test.
	NewTestRuntime func() (globals.RuntimeSource, *TestProvider)
	// NewNilProviderRuntime constructs a runtime source with nil provider.
	NewNilProviderRuntime func() globals.RuntimeSource
}

// RunCreateCobraTests runs the standard suite of create cobra tests.
func RunCreateCobraTests(t *testing.T, spec CreateTestSpec) {
	t.Helper()

	runCmd := func(t *testing.T, rt globals.RuntimeSource, args ...string) (string, error) {
		t.Helper()
		var out bytes.Buffer
		root, optionsSource := BuildTestSubcommand(&out)
		root.AddCommand(spec.NewCmd(rt, optionsSource, &out))
		return RunCommandWithOut(t, root, &out, append([]string{"create"}, args...)...)
	}

	t.Run("DryRunNoAWSCalls", func(t *testing.T) {
		rt, provider := spec.NewTestRuntime()
		got, err := runCmd(t, rt, "--dry-run")
		if err != nil {
			t.Fatalf("dry-run failed: %v", err)
		}
		if provider.CreateCalls != 0 {
			t.Fatalf("dry-run made %d create calls, want 0", provider.CreateCalls)
		}
		AssertContains(t, got, "dry run")
	})

	t.Run("DryRunOutputFields", func(t *testing.T) {
		rt, _ := spec.NewTestRuntime()
		got, err := runCmd(t, rt, "--dry-run")
		if err != nil {
			t.Fatalf("dry-run failed: %v", err)
		}
		for _, want := range append([]string{"123456789012", "us-east-1", "Cost estimate:"}, spec.ResourceNames...) {
			AssertContains(t, got, want)
		}
	})

	t.Run("YesFlagSkipsConfirmation", func(t *testing.T) {
		t.Chdir(t.TempDir())
		rt, provider := spec.NewTestRuntime()
		_, err := runCmd(t, rt, "--yes")
		if err != nil {
			t.Fatalf("--yes run failed: %v", err)
		}
		if provider.CreateCalls != spec.ExpectedCreates {
			t.Fatalf("--yes: expected %d create calls, got %d", spec.ExpectedCreates, provider.CreateCalls)
		}
	})

	t.Run("NilProvider", func(t *testing.T) {
		_, err := runCmd(t, spec.NewNilProviderRuntime())
		if err == nil {
			t.Fatal("expected error when provider is nil")
		}
		AssertContains(t, err.Error(), "no provider configured")
	})

	t.Run("RuntimeError", func(t *testing.T) {
		src := func() (globals.Runtime, error) {
			return globals.Runtime{}, errors.New("config not loaded")
		}
		_, err := runCmd(t, src, "--dry-run")
		if err == nil {
			t.Fatal("expected error when runtimeSource fails")
		}
	})

	if spec.HasInstanceTypeFlag {
		t.Run("InstanceTypeFlag", func(t *testing.T) {
			rt, _ := spec.NewTestRuntime()
			got, err := runCmd(t, rt, "--dry-run", "--instance-type", spec.InstanceTypeValue)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			AssertContains(t, got, spec.InstanceTypeValue)
		})
	}

	if spec.HasVolumeSizeFlag {
		t.Run("VolumeSizeFlag", func(t *testing.T) {
			rt, _ := spec.NewTestRuntime()
			got, err := runCmd(t, rt, "--dry-run", "--volume-size", spec.VolumeSizeValue)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			AssertContains(t, got, spec.VolumeSizeOutput)
		})
	}
}
