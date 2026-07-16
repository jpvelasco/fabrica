package globals_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"

	// Registers the "aws" provider via init() so Store.Init can resolve it.
	_ "github.com/jpvelasco/fabrica/internal/cloud/aws"
)

func TestStoreInit_Defaults_Success(t *testing.T) {
	// A nonexistent config path yields defaults (provider "aws"), which
	// resolves successfully because the aws provider is registered.
	var s globals.Store
	if err := s.Init(filepath.Join(t.TempDir(), "nope.yaml")); err != nil {
		t.Fatalf("Init: %v", err)
	}

	rt, err := s.Require()
	if err != nil {
		t.Fatalf("Require after Init: %v", err)
	}
	if rt.Config == nil {
		t.Error("Runtime.Config is nil")
	}
	if rt.Provider == nil {
		t.Error("Runtime.Provider is nil")
	}
	if rt.Provider.Name() != "aws" {
		t.Errorf("provider = %q, want aws", rt.Provider.Name())
	}
}

func TestStoreInit_UnknownProvider_Error(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	content := "cloud:\n  provider: bogus-cloud\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	var s globals.Store
	err := s.Init(path)
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

func TestStoreInit_MalformedConfig_Error(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	// Invalid YAML so config.Load returns a parse error.
	if err := os.WriteFile(path, []byte("cloud: : : not yaml\n"), 0600); err != nil {
		t.Fatalf("setup write: %v", err)
	}

	var s globals.Store
	if err := s.Init(path); err == nil {
		t.Fatal("expected error for malformed config, got nil")
	}
}

func TestStoreRequire_BeforeInit_Error(t *testing.T) {
	var s globals.Store
	_, err := s.Require()
	if err == nil {
		t.Fatal("expected error when Require is called before Init, got nil")
	}
}

func TestRuntimeConfigFile_DefaultAndExplicit(t *testing.T) {
	if got := (globals.Runtime{}).ConfigFile(); got != config.DefaultFile {
		t.Errorf("ConfigFile() with empty path = %q, want %q", got, config.DefaultFile)
	}

	rt := globals.Runtime{ConfigPath: "custom/fabrica-prod.yaml"}
	if got := rt.ConfigFile(); got != "custom/fabrica-prod.yaml" {
		t.Errorf("ConfigFile() = %q, want custom/fabrica-prod.yaml", got)
	}
}
