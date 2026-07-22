package status_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/status"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{
		Use:           "fabrica",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.SetOut(out)
	root.SetErr(out)

	optionsSource := func() globals.Options { return opts }
	root.AddCommand(status.New(runtimeSource, optionsSource, out))
	return root
}

func runStatus(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"status"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newCobraRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

// TestStatusCobraNotProvisioned verifies clean exit and message when no state exists.
func TestStatusCobraNotProvisioned(t *testing.T) {
	got, err := runStatus(t, newCobraRuntime(&cobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "not provisioned")
}

// TestStatusCobraJSONFlag verifies --json produces parseable JSON output.
func TestStatusCobraJSONFlag(t *testing.T) {
	got, err := runStatus(t, newCobraRuntime(&cobraFakeProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result status.StatusOutput
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if result.Provisioned {
		t.Error("expected provisioned=false when no state exists")
	}
}

// TestStatusCobraNilProvider verifies nil provider does not panic.
func TestStatusCobraNilProvider(t *testing.T) {
	rt := globals.Runtime{Config: config.Defaults(), Provider: nil}
	src := func() (globals.Runtime, error) { return rt, nil }
	got, err := runStatus(t, src)
	if err != nil {
		t.Fatalf("nil provider: unexpected error: %v", err)
	}
	assertCobraContains(t, got, "not provisioned")
}

// TestStatusCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestStatusCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runStatus(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestStatusCobraWaitFlagAccepted verifies --wait/-w flag is accepted (no parse error).
func TestStatusCobraWaitFlagAccepted(t *testing.T) {
	for _, flag := range []string{"--wait", "-w"} {
		t.Run(flag, func(t *testing.T) {
			t.Chdir(t.TempDir())
			_, err := runStatus(t, newCobraRuntime(&cobraFakeProvider{}), flag)
			if err != nil {
				t.Fatalf("%s flag caused error: %v", flag, err)
			}
		})
	}
}

// TestStatusCobraJSONProvisioned verifies --json output with provisioned=true when state exists on disk.
func TestStatusCobraJSONProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stateJSON := `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"horde","version":"","status":"provisioning","resources":[
			{"typeName":"AWS::EC2::SecurityGroup","identifier":"sg-horde"},
			{"typeName":"AWS::EC2::Instance","identifier":"i-horde"}
		]}]}`
	if err := writeStateFile(dir, stateJSON); err != nil {
		t.Fatal(err)
	}

	got, err := runStatus(t, newCobraRuntime(&cobraFakeProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result status.StatusOutput
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if !result.Provisioned {
		t.Error("expected provisioned=true when state has horde module")
	}
	if result.InstanceID != "i-horde" {
		t.Errorf("instanceId = %q, want i-horde", result.InstanceID)
	}
}

// ---- cobraFakeProvider ----

type cobraFakeProvider struct{}

func (f *cobraFakeProvider) Name() string { return "fake" }

func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *cobraFakeProvider) Resources() cloud.ResourceClient {
	return &cobraFakeRC{}
}

type cobraFakeRC struct{}

func (r *cobraFakeRC) Create(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *cobraFakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}

func writeStateFile(dir, content string) error {
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(dir+"/.fabrica", 0700); err != nil {
		return err
	}
	return os.WriteFile(dir+"/.fabrica/state.json", []byte(content), 0600)
}

func assertCobraContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}
