package create_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/lore/create"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(create.New(runtimeSource, optionsSource, out))
	return root
}

func runCreate(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"create"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newCobraTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	cfg.Lore.AmiID = "ami-test123"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

func TestCreateCobraDryRunNoAWSCalls(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("dry-run made %d create calls, want 0", provider.createCalls)
	}
	if !strings.Contains(got, "dry run") {
		t.Fatalf("output missing dry run:\n%s", got)
	}
}

func TestCreateCobraYesFlagSkipsConfirmation(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &cobraFakeProvider{}
	_, err := runCreate(t, newCobraTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("--yes run failed: %v", err)
	}
	if provider.createCalls != 2 {
		t.Fatalf("--yes: expected 2 create calls, got %d", provider.createCalls)
	}
}

func TestCreateCobraNilProvider(t *testing.T) {
	cfg := config.Defaults()
	cfg.Lore.AmiID = "ami-test123"
	rt := globals.Runtime{Config: cfg, Provider: nil}
	_, err := runCreate(t, func() (globals.Runtime, error) { return rt, nil })
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no provider configured") {
		t.Fatalf("error = %v", err)
	}
}

type cobraFakeProvider struct {
	createCalls int
}

func (f *cobraFakeProvider) Name() string { return "fake" }

func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *cobraFakeProvider) Resources() cloud.ResourceClient {
	return &cobraFakeRC{provider: f}
}

type cobraFakeRC struct{ provider *cobraFakeProvider }

func (r *cobraFakeRC) Create(_ context.Context, res *cloud.Resource) error {
	r.provider.createCalls++
	switch res.TypeName {
	case "AWS::EC2::SecurityGroup":
		res.Identifier = fmt.Sprintf("sg-cobra%04d", r.provider.createCalls)
	case "AWS::EC2::Instance":
		res.Identifier = fmt.Sprintf("i-cobra%04d", r.provider.createCalls)
	}
	return nil
}

func (r *cobraFakeRC) Get(_ context.Context, _ *cloud.Resource) error             { return nil }
func (r *cobraFakeRC) Update(_ context.Context, _ *cloud.Resource) error          { return nil }
func (r *cobraFakeRC) Delete(_ context.Context, _ *cloud.Resource) error          { return nil }
func (r *cobraFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) { return nil, nil }
