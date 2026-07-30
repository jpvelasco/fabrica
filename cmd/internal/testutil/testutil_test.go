package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

func TestBuildTestRoot(t *testing.T) {
	var out bytes.Buffer
	root, opts := BuildTestRoot(&out)

	if root.Use != "fabrica" {
		t.Errorf("root.Use = %q, want fabrica", root.Use)
	}
	if opts == nil {
		t.Fatal("opts should not be nil")
		return
	}
	if len(root.Commands()) != 0 {
		t.Error("root should have no subcommands initially")
	}
	if root.PersistentFlags().Lookup("dry-run") == nil {
		t.Error("--dry-run flag missing")
	}
	if root.PersistentFlags().Lookup("yes") == nil {
		t.Error("--yes flag missing")
	}
	if root.PersistentFlags().Lookup("json") == nil {
		t.Error("--json flag missing")
	}
	// Verify opts is wired to flags.
	if opts.DryRun {
		t.Error("DryRun should default to false")
	}
	// Add a test subcommand to verify AddCommand works.
	root.AddCommand(&cobra.Command{Use: "test"})
	if len(root.Commands()) != 1 {
		t.Error("subcommand not added")
	}
}

func TestBuildTestSubcommand(t *testing.T) {
	var out bytes.Buffer
	root, optionsSource := BuildTestSubcommand(&out)

	if root.Use != "fabrica" {
		t.Errorf("root.Use = %q, want fabrica", root.Use)
	}
	if optionsSource == nil {
		t.Fatal("optionsSource should not be nil")
		return
	}
	// Verify the returned optionsSource closure works.
	opts := optionsSource()
	if opts.DryRun {
		t.Error("DryRun should default to false")
	}
	// Verify flags are on the root.
	if root.PersistentFlags().Lookup("dry-run") == nil {
		t.Error("--dry-run flag missing")
	}
	if root.PersistentFlags().Lookup("yes") == nil {
		t.Error("--yes flag missing")
	}
	if root.PersistentFlags().Lookup("json") == nil {
		t.Error("--json flag missing")
	}
	// Verify we can add a subcommand.
	root.AddCommand(&cobra.Command{Use: "test"})
	if len(root.Commands()) != 1 {
		t.Error("subcommand not added")
	}
}

func TestRunCommandWithOut(t *testing.T) {
	var out bytes.Buffer
	root := &cobra.Command{
		Use:           "test",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Fprintln(&out, "hello from command")
			return nil
		},
	}
	got, err := RunCommandWithOut(t, root, &out, "arg1", "arg2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "hello from command") {
		t.Errorf("output %q does not contain expected text", got)
	}
}

func TestRunCommandWithOutError(t *testing.T) {
	var out bytes.Buffer
	root := &cobra.Command{
		Use:           "test",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return fmt.Errorf("simulated failure")
		},
	}
	_, err := RunCommandWithOut(t, root, &out)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTestVPCResolver(t *testing.T) {
	r := &TestVPCResolver{VPCID: "vpc-123", SubnetID: "subnet-456"}
	vpc, subnet, err := r.ResolveDefaultVPC(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vpc != "vpc-123" {
		t.Errorf("VPCID = %q, want vpc-123", vpc)
	}
	if subnet != "subnet-456" {
		t.Errorf("SubnetID = %q, want subnet-456", subnet)
	}
	if r.Calls != 1 {
		t.Errorf("Calls = %d, want 1", r.Calls)
	}
}

func TestTestVPCResolverError(t *testing.T) {
	r := &TestVPCResolver{Err: cloud.ErrResourceNotFound}
	_, _, err := r.ResolveDefaultVPC(context.Background())
	if err != cloud.ErrResourceNotFound {
		t.Fatalf("expected ErrResourceNotFound, got: %v", err)
	}
	if r.Calls != 1 {
		t.Errorf("Calls = %d, want 1", r.Calls)
	}
}

func TestNewTestRuntime(t *testing.T) {
	fake := &TestProvider{}
	src := NewTestRuntime(fake)
	rt, err := src()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.Provider != fake {
		t.Error("provider not set")
	}
	if rt.Config.Cloud.AWS.AccountID != "123456789012" {
		t.Errorf("AccountID = %q, want 123456789012", rt.Config.Cloud.AWS.AccountID)
	}
}

func TestNewNilProviderRuntime(t *testing.T) {
	src := NewNilProviderRuntime()
	rt, err := src()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.Provider != nil {
		t.Error("provider should be nil")
	}
}

func TestWriteStateFile(t *testing.T) {
	t.Run("happy_path", func(t *testing.T) {
		dir := t.TempDir()
		expected := `{"test":true}`
		WriteStateFile(t, dir, expected)

		// Verify the file exists at the expected location with correct size and
		// content. The path is constructed from our own TempDir — trusted input.
		path := filepath.Join(dir, ".fabrica", "state.json")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("state file not created: %v", err)
		}
		if info.Size() != int64(len(expected)) {
			t.Fatalf("unexpected file size: got %d, want %d", info.Size(), len(expected))
		}
		// Validate the resolved path stays within the trusted temp directory.
		absDir, err := filepath.Abs(dir)
		if err != nil {
			t.Fatalf("Abs(dir): %v", err)
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("Abs(path): %v", err)
		}
		if !strings.HasPrefix(absPath, absDir) {
			t.Fatalf("path %q escapes temp dir %q", path, dir)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(data) != expected {
			t.Fatalf("content = %q, want %q", string(data), expected)
		}
	})

	t.Run("mkdir_error", func(t *testing.T) {
		dir := t.TempDir()
		blocker := filepath.Join(dir, "is_a_file")
		if err := os.WriteFile(blocker, nil, 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}
		var gotFatal bool
		fake := &fatalerFake{call: func(...any) { gotFatal = true }}
		writeStateFileAt(fake, blocker, `{}`)
		if !gotFatal {
			t.Error("expected Fatal to be called on MkdirAll error")
		}
	})

	t.Run("write_error", func(t *testing.T) {
		dir := t.TempDir()
		stateDir := filepath.Join(dir, ".fabrica")
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- directory requires execute bit for traversal
		if err := os.MkdirAll(stateDir, dirPermOwner); err != nil {
			t.Fatalf("setup: %v", err)
		}
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- directory requires execute bit for traversal
		if err := os.Mkdir(filepath.Join(stateDir, "state.json"), dirPermOwner); err != nil {
			t.Fatalf("setup: %v", err)
		}
		var gotFatal bool
		fake := &fatalerFake{call: func(...any) { gotFatal = true }}
		writeStateFileAt(fake, dir, `{}`)
		if !gotFatal {
			t.Error("expected Fatal to be called on WriteFile error")
		}
	})
}

// fatalerFake implements fataler for testing writeStateFileAt error branches
// without *testing.T.Fatal exiting the process.
type fatalerFake struct {
	call func(...any)
}

func (f *fatalerFake) Helper() {}

func (f *fatalerFake) Fatal(args ...any) {
	if f.call != nil {
		f.call(args...)
	}
}

func TestAssertContains(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		substr string
	}{
		{name: "substring", text: "hello world", substr: "world"},
		{name: "exact", text: "hello", substr: "hello"},
		{name: "prefix", text: "hello world", substr: "hello"},
		{name: "empty substring", text: "hello", substr: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			AssertContains(t, tt.text, tt.substr)
		})
	}
}

func TestAssertContainsFails(t *testing.T) {
	fake := &assertionTFake{}
	AssertContains(fake, "hello", "goodbye")
	if fake.failure != `"hello" does not contain "goodbye"` {
		t.Fatalf("failure = %q", fake.failure)
	}
}

func TestAssertNotContains(t *testing.T) {
	AssertNotContains(t, "hello world", "goodbye")
}

func TestAssertNotContainsFails(t *testing.T) {
	fake := &assertionTFake{}
	AssertNotContains(fake, "hello world", "world")
	if fake.failure != `"hello world" contains "world"` {
		t.Fatalf("failure = %q", fake.failure)
	}
}

type assertionTFake struct {
	failure string
}

func (f *assertionTFake) Helper() {}

func (f *assertionTFake) Fatalf(format string, args ...any) {
	f.failure = fmt.Sprintf(format, args...)
}

func TestNewProvisionedStateJSON(t *testing.T) {
	got := NewProvisionedStateJSON(StateModule{
		Name: "perforce", Version: "2024.2", Status: "provisioning",
		Resources: EC2Pair("sg-cobra123", "i-cobra123"),
	})

	var parsed struct {
		Account string `json:"account"`
		Region  string `json:"region"`
		Modules []struct {
			Name      string `json:"name"`
			Version   string `json:"version"`
			Status    string `json:"status"`
			Resources []struct {
				TypeName   string         `json:"typeName"`
				Identifier string         `json:"identifier"`
				Properties map[string]any `json:"properties"`
			} `json:"resources"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if parsed.Account != "123456789012" {
		t.Errorf("account = %q", parsed.Account)
	}
	if parsed.Region != "us-east-1" {
		t.Errorf("region = %q", parsed.Region)
	}
	if len(parsed.Modules) != 1 {
		t.Fatalf("modules len = %d, want 1", len(parsed.Modules))
	}
	m := parsed.Modules[0]
	if m.Name != "perforce" || m.Version != "2024.2" || m.Status != "provisioning" {
		t.Errorf("module = %+v", m)
	}
	if len(m.Resources) != 2 {
		t.Fatalf("resources len = %d, want 2", len(m.Resources))
	}
	if m.Resources[0].TypeName != "AWS::EC2::SecurityGroup" || m.Resources[0].Identifier != "sg-cobra123" {
		t.Errorf("resource[0] = %+v", m.Resources[0])
	}
	if m.Resources[1].TypeName != "AWS::EC2::Instance" || m.Resources[1].Identifier != "i-cobra123" {
		t.Errorf("resource[1] = %+v", m.Resources[1])
	}
}

func TestNewProvisionedStateJSONWithProperties(t *testing.T) {
	got := NewProvisionedStateJSON(StateModule{
		Name: "ddc", Version: "ami-ddc123", Status: "ready",
		Resources: []StateResource{
			{
				TypeName: "AWS::EC2::Instance", Identifier: "i-coord123",
				Properties: map[string]any{"role": "coordinator"},
			},
		},
	})
	var parsed struct {
		Modules []struct {
			Resources []struct {
				Properties map[string]any `json:"properties"`
			} `json:"resources"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.Modules[0].Resources[0].Properties["role"] != "coordinator" {
		t.Errorf("properties = %+v", parsed.Modules[0].Resources[0].Properties)
	}
}

func TestNewProvisionedStateJSONMultiModule(t *testing.T) {
	got := NewProvisionedStateJSON(
		StateModule{Name: "ci", Version: "fabrica-ci", Status: "ready"},
		StateModule{Name: "horde", Version: "ami-1", Status: "ready"},
	)
	var parsed struct {
		Modules []struct {
			Name string `json:"name"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(parsed.Modules) != 2 {
		t.Fatalf("modules len = %d, want 2", len(parsed.Modules))
	}
	if parsed.Modules[0].Name != "ci" || parsed.Modules[1].Name != "horde" {
		t.Errorf("module names = %q, %q", parsed.Modules[0].Name, parsed.Modules[1].Name)
	}
}

func TestEC2Pair(t *testing.T) {
	pair := EC2Pair("sg-1", "i-1")
	if len(pair) != 2 {
		t.Fatalf("len = %d, want 2", len(pair))
	}
	if pair[0].TypeName != "AWS::EC2::SecurityGroup" || pair[0].Identifier != "sg-1" {
		t.Errorf("sg = %+v", pair[0])
	}
	if pair[1].TypeName != "AWS::EC2::Instance" || pair[1].Identifier != "i-1" {
		t.Errorf("instance = %+v", pair[1])
	}
}

func TestNewProvisionedStateJSONEmptyModules(t *testing.T) {
	got := NewProvisionedStateJSON()
	var parsed struct {
		Account string `json:"account"`
		Modules []any  `json:"modules"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.Account != "123456789012" {
		t.Errorf("account = %q", parsed.Account)
	}
	if len(parsed.Modules) != 0 {
		t.Errorf("modules = %v, want empty", parsed.Modules)
	}
}

// TestNewProvisionedStateJSONMarshalPanic covers the defensive panic when
// Properties holds a non-JSON-encodable value (channels cannot be marshaled).
func TestNewProvisionedStateJSONMarshalPanic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on unencodable Properties")
			return
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "NewProvisionedStateJSON") {
			t.Fatalf("panic = %v, want NewProvisionedStateJSON message", r)
		}
	}()
	_ = NewProvisionedStateJSON(StateModule{
		Name: "x", Version: "1", Status: "ready",
		Resources: []StateResource{{
			TypeName: "AWS::EC2::Instance", Identifier: "i-1",
			Properties: map[string]any{"bad": make(chan int)},
		}},
	})
}

// TestParseBaseStatusOutput exercises the shared JSON parser.
func TestParseBaseStatusOutput(t *testing.T) {
	t.Run("provisioned", func(t *testing.T) {
		prov, id, err := ParseBaseStatusOutput(`{"provisioned":true,"instanceId":"i-123"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !prov || id != "i-123" {
			t.Errorf("prov=%v id=%q, want true i-123", prov, id)
		}
	})

	t.Run("not-provisioned", func(t *testing.T) {
		prov, _, err := ParseBaseStatusOutput(`{"provisioned":false}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prov {
			t.Error("expected provisioned=false")
		}
	})

	t.Run("invalid-json", func(t *testing.T) {
		_, _, err := ParseBaseStatusOutput(`not json`)
		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

// TestBuildTestRuntime exercises the status test convenience.
func TestBuildTestRuntime(t *testing.T) {
	t.Run("with-provider", func(t *testing.T) {
		p := &TestProvider{}
		src := BuildTestRuntime(p)
		rt, err := src()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rt.Provider == nil {
			t.Error("expected non-nil provider")
		}
	})

	t.Run("nil-provider", func(t *testing.T) {
		src := BuildTestRuntime(nil)
		rt, err := src()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rt.Provider != nil {
			t.Error("expected nil provider")
		}
	})
}

// TestUbuntuAMIProviderDefault exercises the default AMI fallback.
func TestUbuntuAMIProviderDefault(t *testing.T) {
	p := &UbuntuAMIProvider{}
	ami, err := p.ResolveUbuntuAMI(context.Background(), "22.04")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ami != "ami-fake-ubuntu" {
		t.Errorf("ami = %q, want %q", ami, "ami-fake-ubuntu")
	}
}

// TestUbuntuAMIProviderError exercises the error path.
func TestUbuntuAMIProviderError(t *testing.T) {
	p := &UbuntuAMIProvider{AMIErr: fmt.Errorf("not found")}
	_, err := p.ResolveUbuntuAMI(context.Background(), "22.04")
	if err == nil {
		t.Fatal("expected error")
	}
}
