// Package testutil provides shared helpers for cobra black-box tests.
//
// Every cobra_test.go file needs the same scaffolding: a minimal root command
// with persistent flags, a fake provider, state file writers, and output
// assertions. This package centralises that boilerplate so the per-command
// tests focus on their actual logic.
//
// Module-specific fakes (CodeBuildRunner, GameLiftManager, EC2InstanceManager)
// stay local to the test file that needs them — only the generic Provider/
// ResourceClient shape is shared here.
package testutil

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// dirPermOwner is the least-privilege permission for a directory — owner-only
// rwx. The execute bit is required for directory traversal; this is not
// analogous to file permissions (which use 0o600).
const dirPermOwner = 0o700

// BuildTestRoot creates a minimal root cobra command with the standard
// persistent flags (--dry-run, --yes, --json). It returns the root command
// and a pointer to the shared Options struct so the caller can wire
// optionsSource on their subcommand.
//
// Usage:
//
//	root, opts := testutil.BuildTestRoot(&out)
//	optionsSource := func() globals.Options { return *opts }
//	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
//	root.SetArgs([]string{"destroy", "--yes"})
//	err := root.ExecuteContext(ctx)
func BuildTestRoot(out *bytes.Buffer) (*cobra.Command, *globals.Options) {
	opts := &globals.Options{}
	root := &cobra.Command{
		Use:           "fabrica",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.SetOut(out)
	root.SetErr(out)

	return root, opts
}

// NewTestRuntime creates a runtime source with the given provider and a default
// config (account ID set to 123456789012).
func NewTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

// NewNilProviderRuntime creates a runtime source with no provider (nil).
// Useful for testing the "not provisioned" and "no provider" paths.
func NewNilProviderRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	return func() (globals.Runtime, error) { return rt, nil }
}

// writeStateFileAt creates the .fabrica directory under dir and writes content
// to state.json. Returns an error on failure instead of calling t.Fatal,
// allowing error-branch coverage in tests.
func writeStateFileAt(dir, content string) error {
	stateDir := filepath.Join(dir, ".fabrica")
	if err := os.MkdirAll(stateDir, dirPermOwner); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(content), 0o600)
}

// WriteStateFile writes JSON content to .fabrica/state.json in the given directory.
// Creates the .fabrica directory if needed.
//
// Directories require the execute bit for traversal (dirPermOwner), which is the
// least-privilege permission for a directory. File writes use 0o600.
func WriteStateFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := writeStateFileAt(dir, content); err != nil {
		t.Fatal(err)
	}
}

// AssertContains checks that s contains substr and fails the test if not.
func AssertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Fatalf("%q does not contain %q", s, substr)
	}
}

// CobraFakeProvider is a minimal fake provider that tracks delete calls.
// It satisfies cloud.Provider and provides a CobraFakeRC as its ResourceClient.
type CobraFakeProvider struct {
	DeleteCalls  int
	CreateCalls  int
	IdentErr     error
	GetResources map[string]cloud.Resource
}

func (f *CobraFakeProvider) Name() string { return "fake" }

func (f *CobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	if f.IdentErr != nil {
		return "", "", "", f.IdentErr
	}
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *CobraFakeProvider) Resources() cloud.ResourceClient {
	return &CobraFakeRC{provider: f}
}

// CobraFakeRC is a minimal fake ResourceClient backed by CobraFakeProvider.
type CobraFakeRC struct {
	provider *CobraFakeProvider
}

func (r *CobraFakeRC) Create(_ context.Context, res *cloud.Resource) error {
	r.provider.CreateCalls++
	if res != nil {
		// Assign a default identifier for test assertions.
		if res.Identifier == "" {
			switch res.TypeName {
			case "AWS::EC2::Instance":
				res.Identifier = "i-cobra123"
			case "AWS::EC2::SecurityGroup":
				res.Identifier = "sg-cobra123"
			case "AWS::IAM::Role":
				res.Identifier = "arn:aws:iam::123456789012:role/test-role"
			default:
				res.Identifier = "test-" + res.TypeName
			}
		}
	}
	return nil
}

func (r *CobraFakeRC) Get(_ context.Context, res *cloud.Resource) error {
	if res == nil {
		return cloud.ErrResourceNotFound
	}
	if r.provider.GetResources != nil {
		if stored, ok := r.provider.GetResources[res.TypeName]; ok {
			res.Identifier = stored.Identifier
			res.ActualState = stored.ActualState
		}
	}
	return nil
}

func (r *CobraFakeRC) Update(_ context.Context, _ *cloud.Resource) error {
	return nil
}

func (r *CobraFakeRC) Delete(_ context.Context, _ *cloud.Resource) error {
	r.provider.DeleteCalls++
	return nil
}

func (r *CobraFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}
