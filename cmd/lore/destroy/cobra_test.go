package destroy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/lore/destroy"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func TestDestroyCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	var opts globals.Options
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	root.AddCommand(destroy.New(
		func() (globals.Runtime, error) { return rt, nil },
		func() globals.Options { return opts },
		&out,
	))
	root.SetArgs([]string{"destroy", "--yes"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	testutil.AssertContains(t, out.String(), "not provisioned")
}

func TestNewTeardownWiring(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	tc := destroy.NewTeardown(rt, io.Discard)
	if tc.Spec.ModuleName != "lore" {
		t.Errorf("ModuleName = %q", tc.Spec.ModuleName)
	}
	if !tc.SkipConfirm {
		t.Error("SkipConfirm should be true for orchestrated teardown")
	}
}

// ---- Helper builders ----

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.SetOut(out)
	root.SetErr(out)
	optionsSource := func() globals.Options { return opts }
	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return root
}

func loreStateJSON() string {
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"lore","version":"ami-0abc123","status":"provisioning","resources":[
			{"typeName":"AWS::EC2::SecurityGroup","identifier":"sg-lore123"},
			{"typeName":"AWS::EC2::Instance","identifier":"i-lore123"}
		]}]}`
}

// ---- Cobra tests with provider ----

func TestDestroyCobraDryRunWithProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, loreStateJSON())
	var out bytes.Buffer
	root := buildTestRoot(testutil.NewTestRuntime(&testutil.CobraFakeProvider{}), &out)
	root.SetArgs([]string{"destroy", "--dry-run"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	got := out.String()
	testutil.AssertContains(t, got, "dry run")
	testutil.AssertContains(t, got, "i-lore123")
}

func TestDestroyCobraYesWithProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, loreStateJSON())
	provider := &testutil.CobraFakeProvider{}
	var out bytes.Buffer
	root := buildTestRoot(testutil.NewTestRuntime(provider), &out)
	root.SetArgs([]string{"destroy", "--yes"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("destroy --yes: %v", err)
	}
	if provider.DeleteCalls != 2 {
		t.Errorf("expected 2 delete calls, got %d", provider.DeleteCalls)
	}
}

func TestDestroyCobraJSONDryRunWithProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, loreStateJSON())
	var out bytes.Buffer
	root := buildTestRoot(testutil.NewTestRuntime(&testutil.CobraFakeProvider{}), &out)
	root.SetArgs([]string{"destroy", "--json", "--dry-run"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("json dry-run: %v", err)
	}
	var result teardown.Output
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if !result.DryRun {
		t.Error("dryRun must be true")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 resources in dry run, got %d", len(result.Destroyed))
	}
}

func TestDestroyCobraJSONYesWithProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, loreStateJSON())
	var out bytes.Buffer
	root := buildTestRoot(testutil.NewTestRuntime(&testutil.CobraFakeProvider{}), &out)
	root.SetArgs([]string{"destroy", "--json", "--yes"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("json yes: %v", err)
	}
	var result teardown.Output
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.DryRun {
		t.Error("dryRun must be false")
	}
}

func TestDestroyCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	var out bytes.Buffer
	root := buildTestRoot(src, &out)
	root.SetArgs([]string{"destroy", "--yes"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// ---- NewTeardown with provider ----

func TestNewTeardownWiringWithProvider(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: &testutil.CobraFakeProvider{}}
	tc := destroy.NewTeardown(rt, io.Discard)
	if !tc.SkipConfirm || !tc.AssumeYes {
		t.Fatalf("SkipConfirm/AssumeYes must be true; got SkipConfirm=%v, AssumeYes=%v", tc.SkipConfirm, tc.AssumeYes)
	}
	if tc.DeleteResource == nil {
		t.Error("DeleteResource must be wired when provider is non-nil")
	}
	if tc.GetResource == nil {
		t.Error("GetResource must be wired when provider is non-nil")
	}
	if tc.Spec.ModuleName != "lore" {
		t.Errorf("module name = %q, want lore", tc.Spec.ModuleName)
	}
}
