package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Cloud.Provider != "aws" {
		t.Fatalf("expected provider aws, got %s", cfg.Cloud.Provider)
	}
	if cfg.Cloud.AWS.Region != "us-east-1" {
		t.Fatalf("expected region us-east-1, got %s", cfg.Cloud.AWS.Region)
	}
	if cfg.State.Table != "fabrica-state-lock" {
		t.Fatalf("expected table fabrica-state-lock, got %s", cfg.State.Table)
	}
	if len(cfg.Cloud.AWS.Tags) != 0 {
		t.Fatal("expected empty tags map")
	}
}

func TestClone(t *testing.T) {
	cfg := Defaults()
	cfg.Cloud.AWS.Tags["foo"] = "bar"

	cl := cfg.Clone()
	if cl.Cloud.AWS.Tags["foo"] != "bar" {
		t.Fatal("clone missing copied tag")
	}
	// Mutate clone must not affect original
	cl.Cloud.AWS.Tags["foo"] = "baz"
	if cfg.Cloud.AWS.Tags["foo"] != "bar" {
		t.Fatal("clone mutation leaked into original")
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load("nonexistent.yaml")
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	// Should return defaults
	if cfg.Cloud.Provider != "aws" {
		t.Fatalf("expected defaults, got provider %s", cfg.Cloud.Provider)
	}
}

func TestLoadValidFile(t *testing.T) {
	dir := t.TempDir()
	content := `cloud:
  provider: aws
  aws:
    region: eu-west-1
    profile: my-profile
    accountId: "123456789012"
    tags:
      env: staging

state:
  bucket: my-fabrica-state
  table: custom-lock
  kmsKeyId: alias/fabrica-key
`
	path := filepath.Join(dir, "fabrica.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Cloud.Provider != "aws" {
		t.Errorf("provider = %q, want aws", cfg.Cloud.Provider)
	}
	if cfg.Cloud.AWS.Region != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1", cfg.Cloud.AWS.Region)
	}
	if cfg.Cloud.AWS.Profile != "my-profile" {
		t.Errorf("profile = %q, want my-profile", cfg.Cloud.AWS.Profile)
	}
	if cfg.Cloud.AWS.AccountID != "123456789012" {
		t.Errorf("accountId = %q, want 123456789012", cfg.Cloud.AWS.AccountID)
	}
	if cfg.Cloud.AWS.Tags["env"] != "staging" {
		t.Errorf("tags.env = %q, want staging", cfg.Cloud.AWS.Tags["env"])
	}
	if cfg.State.Bucket != "my-fabrica-state" {
		t.Errorf("bucket = %q, want my-fabrica-state", cfg.State.Bucket)
	}
	if cfg.State.Table != "custom-lock" {
		t.Errorf("table = %q, want custom-lock", cfg.State.Table)
	}
	if cfg.State.KMSKeyID != "alias/fabrica-key" {
		t.Errorf("kmsKeyId = %q, want alias/fabrica-key", cfg.State.KMSKeyID)
	}
}

func TestLoadPartialFile(t *testing.T) {
	dir := t.TempDir()
	content := `cloud:
  aws:
    region: ap-southeast-1
`
	path := filepath.Join(dir, "fabrica.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Region overridden from file
	if cfg.Cloud.AWS.Region != "ap-southeast-1" {
		t.Errorf("region = %q, want ap-southeast-1", cfg.Cloud.AWS.Region)
	}
	// Provider defaults preserved
	if cfg.Cloud.Provider != "aws" {
		t.Errorf("provider = %q, want aws", cfg.Cloud.Provider)
	}
	// Table defaults preserved
	if cfg.State.Table != "fabrica-state-lock" {
		t.Errorf("table = %q, want fabrica-state-lock", cfg.State.Table)
	}
}
