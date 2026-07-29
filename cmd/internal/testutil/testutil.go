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
	"encoding/json"
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
//
//nolint:gomnd // directory permission constant
const dirPermOwner = 0o700

// fataler is the minimal subset of testing.T needed for WriteStateFile.
// *testing.T satisfies this interface implicitly.
type fataler interface {
	Helper()
	Fatal(...any)
}

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

// writeStateFileAt is the internal implementation of WriteStateFile that
// accepts a fataler interface, enabling error-branch coverage in tests
// without process exit.
func writeStateFileAt(t fataler, dir, content string) {
	stateDir := filepath.Join(dir, ".fabrica")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- directory requires execute bit for traversal
	if err := os.MkdirAll(stateDir, dirPermOwner); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// WriteStateFile writes JSON content to .fabrica/state.json in the given directory.
// Creates the .fabrica directory if needed.
//
// Directories require the execute bit for traversal (dirPermOwner), which is the
// least-privilege permission for a directory. File writes use 0o600.
func WriteStateFile(t *testing.T, dir, content string) {
	t.Helper()
	writeStateFileAt(t, dir, content)
}

// AssertContains checks that s contains substr and fails the test if not.
func AssertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Fatalf("%q does not contain %q", s, substr)
	}
}

// BuildTestSubcommand wires a subcommand into a minimal root command. It is
// designed for use with subcommands that accept a pre-built optionsSource.
// The caller constructs the subcommand using the returned optionsSource closure.
//
// Usage:
//
//	root, optionsSource := testutil.BuildTestSubcommand(&out)
//	sub := mycmd.New(runtimeSource, optionsSource, &out)
//	root.AddCommand(sub)
//	root.SetArgs([]string{"sub", "--yes"})
//	err := root.ExecuteContext(ctx)
func BuildTestSubcommand(out *bytes.Buffer) (*cobra.Command, globals.OptionsSource) {
	root, opts := BuildTestRoot(out)
	return root, func() globals.Options { return *opts }
}

// RunCommandWithOut executes a root cobra command with the given args and
// returns (output, error). The caller provides the output buffer (shared with
// the subcommand via buildTestRoot). Replaces the ~5-line runDestroy/
// runTerminate helpers scattered across destroy cobra_test.go files.
//
// Usage:
//
//	var out bytes.Buffer
//	root := buildTestRoot(runtimeSource, &out)
//	got, err := testutil.RunCommandWithOut(t, root, &out, "--yes")
func RunCommandWithOut(t *testing.T, root *cobra.Command, out *bytes.Buffer, args ...string) (string, error) {
	t.Helper()
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// TestVPCResolver is a shared fake VPC resolver for plan-layer tests.
// It implements the VPCResolver interface used by perforce, horde, lore,
// and workstation plan tests. The Calls field tracks how many times
// ResolveDefaultVPC was invoked (replaces ad-hoc callTracker fields).
type TestVPCResolver struct {
	VPCID    string
	SubnetID string
	Err      error
	Calls    int
}

func (f *TestVPCResolver) ResolveDefaultVPC(_ context.Context) (string, string, error) {
	f.Calls++
	return f.VPCID, f.SubnetID, f.Err
}

// StateResource describes one resource entry in a provisioned state fixture.
type StateResource struct {
	TypeName   string
	Identifier string
	// Properties is optional resource metadata (e.g. role=coordinator for DDC).
	// When nil, the properties field is omitted from the JSON.
	Properties map[string]any
}

// StateModule describes one module entry in a provisioned state fixture.
type StateModule struct {
	Name      string
	Version   string
	Status    string
	Resources []StateResource
}

// NewProvisionedStateJSON builds a `.fabrica/state.json` body for cobra tests.
// Account is always "123456789012" and region always "us-east-1" (matches
// NewTestRuntime). Replaces the hand-crafted provisionedStateJSON / *StateJSON
// helpers that were copy-pasted across cobra_test.go files.
//
// Usage:
//
//	testutil.WriteStateFile(t, dir, testutil.NewProvisionedStateJSON(
//	    testutil.StateModule{
//	        Name: "perforce", Version: "2024.2", Status: "provisioning",
//	        Resources: []testutil.StateResource{
//	            {TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-cobra123"},
//	            {TypeName: "AWS::EC2::Instance", Identifier: "i-cobra123"},
//	        },
//	    },
//	))
func NewProvisionedStateJSON(modules ...StateModule) string {
	type resJSON struct {
		TypeName   string         `json:"typeName"`
		Identifier string         `json:"identifier"`
		Properties map[string]any `json:"properties,omitempty"`
	}
	type modJSON struct {
		Name      string    `json:"name"`
		Version   string    `json:"version"`
		Status    string    `json:"status"`
		Resources []resJSON `json:"resources"`
	}
	type stateJSON struct {
		Account string    `json:"account"`
		Region  string    `json:"region"`
		Modules []modJSON `json:"modules"`
	}

	out := stateJSON{
		Account: "123456789012",
		Region:  "us-east-1",
		Modules: make([]modJSON, 0, len(modules)),
	}
	for _, m := range modules {
		mj := modJSON{
			Name:      m.Name,
			Version:   m.Version,
			Status:    m.Status,
			Resources: make([]resJSON, 0, len(m.Resources)),
		}
		for _, r := range m.Resources {
			mj.Resources = append(mj.Resources, resJSON(r))
		}
		out.Modules = append(out.Modules, mj)
	}
	b, err := json.Marshal(out)
	if err != nil {
		// StateResource fields are plain Go values; marshal only fails on
		// unencodable types which callers must not pass. Panic so tests fail
		// loudly rather than writing corrupt fixtures.
		panic("testutil.NewProvisionedStateJSON: " + err.Error())
	}
	return string(b)
}

// EC2Pair is a convenience constructor for the common SecurityGroup + Instance
// resource pair used by perforce/horde/lore/workstation fixtures.
func EC2Pair(sgID, instanceID string) []StateResource {
	return []StateResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: sgID},
		{TypeName: "AWS::EC2::Instance", Identifier: instanceID},
	}
}
