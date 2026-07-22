package destroy

import (
	"context"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

// ---- Spec validation ----

func TestSpecFields(t *testing.T) {
	if spec.ModuleName != "lore" {
		t.Errorf("ModuleName = %q, want lore", spec.ModuleName)
	}
	if spec.Verb != "destroy" {
		t.Errorf("Verb = %q, want destroy", spec.Verb)
	}
	if spec.VersionLabel != "AMI ID" {
		t.Errorf("VersionLabel = %q", spec.VersionLabel)
	}
	if spec.Title != "Lore loreserver" {
		t.Errorf("Title = %q", spec.Title)
	}
	if spec.NotProvisioned == "" {
		t.Error("NotProvisioned should not be empty")
	}
	if spec.PlanHeader == "" {
		t.Error("PlanHeader should not be empty")
	}
	if spec.DryRunHeader == "" {
		t.Error("DryRunHeader should not be empty")
	}
	if spec.Irreversible == "" {
		t.Error("Irreversible should not be empty")
	}
	if spec.SuccessMessage == "" {
		t.Error("SuccessMessage should not be empty")
	}
}

// ---- NewTeardown ----

func TestNewTeardownNilProvider(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	tc := NewTeardown(rt, io.Discard)

	if tc.Spec.ModuleName != "lore" {
		t.Errorf("ModuleName = %q, want lore", tc.Spec.ModuleName)
	}
	if !tc.SkipConfirm {
		t.Error("SkipConfirm should be true for orchestrated teardown")
	}
	if !tc.AssumeYes {
		t.Error("AssumeYes should be true for orchestrated teardown")
	}
	if tc.ReadState == nil {
		t.Error("ReadState must be wired")
	}
	if tc.WriteState == nil {
		t.Error("WriteState must be wired")
	}
	if tc.Confirm == nil {
		t.Error("Confirm must be wired")
	}
	if tc.DeleteResource != nil {
		t.Error("DeleteResource must be nil when provider is nil")
	}
	if tc.GetResource != nil {
		t.Error("GetResource must be nil when provider is nil")
	}
}

type fakeRC struct{}

func (fakeRC) Create(context.Context, *cloud.Resource) error          { return nil }
func (fakeRC) Get(context.Context, *cloud.Resource) error             { return nil }
func (fakeRC) Update(context.Context, *cloud.Resource) error          { return nil }
func (fakeRC) Delete(context.Context, *cloud.Resource) error          { return nil }
func (fakeRC) List(context.Context, string) ([]cloud.Resource, error) { return nil, nil }

type fakeTeardownProvider struct{}

func (fakeTeardownProvider) Name() string { return "fake" }
func (fakeTeardownProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (fakeTeardownProvider) Resources() cloud.ResourceClient { return fakeRC{} }

func TestNewTeardownWithProvider(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: fakeTeardownProvider{}}
	tc := NewTeardown(rt, io.Discard)

	if tc.Spec.ModuleName != "lore" {
		t.Errorf("ModuleName = %q, want lore", tc.Spec.ModuleName)
	}
	if !tc.SkipConfirm {
		t.Error("SkipConfirm should be true")
	}
	if !tc.AssumeYes {
		t.Error("AssumeYes should be true")
	}
	if tc.DeleteResource == nil {
		t.Error("DeleteResource must be wired when provider is set")
	}
	if tc.GetResource == nil {
		t.Error("GetResource must be wired when provider is set")
	}
}

type nilRCProvider struct{}

func (nilRCProvider) Name() string { return "fake" }
func (nilRCProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (nilRCProvider) Resources() cloud.ResourceClient { return nil }

func TestNewTeardownWithProviderNilRC(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nilRCProvider{}}
	tc := NewTeardown(rt, io.Discard)

	if tc.DeleteResource != nil {
		t.Error("DeleteResource must be nil when Resources() returns nil")
	}
	if tc.GetResource != nil {
		t.Error("GetResource must be nil when Resources() returns nil")
	}
}

// ---- New command ----

func TestNewCommandUse(t *testing.T) {
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults()}, nil
	}
	opts := func() globals.Options { return globals.Options{} }
	cmd := New(rt, opts, io.Discard)

	if cmd.Use != "destroy" {
		t.Errorf("Use = %q, want destroy", cmd.Use)
	}
	if cmd.Short != "Permanently delete the Lore server" {
		t.Errorf("Short = %q", cmd.Short)
	}
	if cmd.Long == "" {
		t.Error("Long should not be empty")
	}
}

func TestNewCommandWithProvider(t *testing.T) {
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: fakeTeardownProvider{}}, nil
	}
	opts := func() globals.Options { return globals.Options{} }
	cmd := New(rt, opts, io.Discard)

	if cmd.RunE == nil {
		t.Error("RunE must be set")
	}
}
