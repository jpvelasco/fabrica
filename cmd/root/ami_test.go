package root_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/root"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

// Calling any provider method on this factory result panics. AMI generation
// must use runtime config without authenticating or constructing SDK clients.
type offlineAMIProvider struct{ cloud.Provider }

func TestAMIBuildRootUsesRuntimeFactory(t *testing.T) {
	providerName := "offline-ami-" + filepath.Base(filepath.Dir(t.TempDir()))
	factoryCalls := 0
	cloud.Register(providerName, func(cfg *config.Config) (cloud.Provider, error) {
		factoryCalls++
		if cfg.Cloud.AWS.Region != "us-west-2" || cfg.Cloud.AWS.Tags["ManagedBy"] != "foreign" {
			return nil, fmt.Errorf("factory received wrong configuration: %+v", cfg.Cloud.AWS)
		}
		return &offlineAMIProvider{}, nil
	})
	for _, module := range []string{"horde", "lore", "workstation"} {
		t.Run(module, func(t *testing.T) {
			runRootAMIBuild(t, module, fmt.Sprintf("cloud:\n  provider: %s\n  aws:\n    region: us-west-2\n    tags:\n      ManagedBy: foreign\n", providerName))
		})
	}
	if factoryCalls != 3 {
		t.Fatalf("factory called %d times, want 3", factoryCalls)
	}
}

func TestAMIBuildRootDoesNotLoadAWSCredentials(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE"} {
		t.Setenv(key, "")
	}
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", empty)
	t.Setenv("AWS_CONFIG_FILE", empty)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	for _, module := range []string{"horde", "lore", "workstation"} {
		t.Run(module, func(t *testing.T) {
			runRootAMIBuild(t, module, "cloud:\n  provider: aws\n  aws:\n    profile: missing-offline-ami-profile\n")
		})
	}
}

func runRootAMIBuild(t *testing.T, module, content string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "fabrica.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(dir, "artifacts")
	var output bytes.Buffer
	cmd := root.New(&output)
	cmd.SetArgs([]string{"--config", configPath, module, "ami", "build", "--output-dir", outputDir})
	if err := cmd.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(outputDir, "image-builder-recipe.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recipe struct {
		Tags map[string]string `json:"tags"`
	}
	if err := json.Unmarshal(raw, &recipe); err != nil {
		t.Fatal(err)
	}
	if recipe.Tags["ManagedBy"] != "fabrica" {
		t.Fatalf("wrong generated ownership: %v", recipe.Tags)
	}
}

func TestAMIBuildRootKeepsCustomActivationGuard(t *testing.T) {
	for _, module := range []string{"horde", "lore", "workstation"} {
		t.Run(module, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "fabrica.yaml")
			if err := os.WriteFile(configPath, []byte("cloud:\n  aws:\n    identity:\n      managedBy: studio-tools\n"), 0600); err != nil {
				t.Fatal(err)
			}
			outputDir := filepath.Join(dir, "artifacts")
			cmd := root.New(&bytes.Buffer{})
			cmd.SilenceErrors = true
			cmd.SetArgs([]string{"--config", configPath, module, "ami", "build", "--output-dir", outputDir})
			err := cmd.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), "custom ownership is not enabled") {
				t.Fatalf("custom configuration escaped activation guard: %v", err)
			}
			if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
				t.Fatalf("guard created output directory: %v", err)
			}
		})
	}
}
