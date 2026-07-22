package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
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

func TestPath(t *testing.T) {
	tests := []struct {
		name     string
		explicit string
		profile  string
		want     string
	}{
		{name: "default", want: ""},
		{name: "explicit wins", explicit: "custom.yaml", profile: "prod", want: "custom.yaml"},
		{name: "profile", profile: "prod", want: "fabrica-prod.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Path(tt.explicit, tt.profile); got != tt.want {
				t.Fatalf("Path() = %q, want %q", got, tt.want)
			}
		})
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
	path := filepath.Clean(filepath.Join(dir, "fabrica.yaml"))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
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

func TestLoadPerforceBackupConfig(t *testing.T) {
	dir := t.TempDir()
	content := `perforce:
  version: "2024.2"
  backup:
    path: /custom/backups
    s3Export: true
    s3Bucket: my-backup-bucket
    s3Prefix: studio/p4/
`
	path := filepath.Clean(filepath.Join(dir, "fabrica.yaml"))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Perforce.Version != "2024.2" {
		t.Errorf("version = %q, want 2024.2", cfg.Perforce.Version)
	}
	b := cfg.Perforce.Backup
	if b.Path != "/custom/backups" {
		t.Errorf("backup.path = %q, want /custom/backups", b.Path)
	}
	if !b.S3Export {
		t.Error("backup.s3Export = false, want true")
	}
	if b.S3Bucket != "my-backup-bucket" {
		t.Errorf("backup.s3Bucket = %q, want my-backup-bucket", b.S3Bucket)
	}
	if b.S3Prefix != "studio/p4/" {
		t.Errorf("backup.s3Prefix = %q, want studio/p4/", b.S3Prefix)
	}
}

func TestLoadPartialFile(t *testing.T) {
	dir := t.TempDir()
	content := `cloud:
  aws:
    region: ap-southeast-1
`
	path := filepath.Clean(filepath.Join(dir, "fabrica.yaml"))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
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

func TestSaveWritesSchemaNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Clean(filepath.Join(dir, "fabrica.yaml"))

	cfg := Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.State.KMSKeyID = "alias/fabrica"

	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(got)
	for _, want := range []string{"accountId:", "kmsKeyId:", "perforce:", "horde:", "ci:", "cost:"} {
		if !contains(text, want) {
			t.Fatalf("saved config missing %q:\n%s", want, text)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestWorkstationConfigDefaults(t *testing.T) {
	cfg := Defaults()
	if cfg.Workstation.InstanceType != "" {
		t.Errorf("expected empty InstanceType default, got %q", cfg.Workstation.InstanceType)
	}
}

func TestWorkstationConfigUnmarshal(t *testing.T) {
	yaml := `
workstation:
  amiId: ami-12345678
  instanceType: g4dn.xlarge
  volumeSize: 200
  vpcId: vpc-abc
  subnetId: subnet-def
  idleTimeoutMinutes: 30
  allowedCidr: 10.0.0.0/8
`
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(yaml)); err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	cfg := Defaults()
	if err := v.Unmarshal(cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Workstation.AmiID != "ami-12345678" {
		t.Errorf("AmiID = %q, want ami-12345678", cfg.Workstation.AmiID)
	}
	if cfg.Workstation.InstanceType != "g4dn.xlarge" {
		t.Errorf("InstanceType = %q, want g4dn.xlarge", cfg.Workstation.InstanceType)
	}
	if cfg.Workstation.VolumeSize != 200 {
		t.Errorf("VolumeSize = %d, want 200", cfg.Workstation.VolumeSize)
	}
	if cfg.Workstation.IdleTimeoutMinutes != 30 {
		t.Errorf("IdleTimeoutMinutes = %d, want 30", cfg.Workstation.IdleTimeoutMinutes)
	}
	if cfg.Workstation.AllowedCIDR != "10.0.0.0/8" {
		t.Errorf("AllowedCIDR = %q, want 10.0.0.0/8", cfg.Workstation.AllowedCIDR)
	}
}

func TestLoadDeployConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Clean(filepath.Join(dir, "fabrica.yaml"))
	if err := os.WriteFile(path, []byte(`
cloud:
  provider: aws
deploy:
  instanceType: c5.large
  fleetType: ON_DEMAND
  launchPath: /local/game/ServerApp
  buildBucket: my-build-bucket
  desiredInstances: 2
  activationTimeoutMinutes: 30
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Deploy.InstanceType != "c5.large" {
		t.Errorf("InstanceType = %q", cfg.Deploy.InstanceType)
	}
	if cfg.Deploy.DesiredInstances != 2 {
		t.Errorf("DesiredInstances = %d", cfg.Deploy.DesiredInstances)
	}
	if cfg.Deploy.ActivationTimeoutMinutes != 30 {
		t.Errorf("ActivationTimeoutMinutes = %d", cfg.Deploy.ActivationTimeoutMinutes)
	}
}

func TestCostConfigRoundTrip(t *testing.T) {
	c := Defaults()
	c.Cost.Budgets = []BudgetThreshold{
		{Scope: "total", Monthly: 400, WarnPct: 80},
		{Scope: "perforce", Monthly: 150},
	}
	data, err := c.YAML()
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	if !strings.Contains(string(data), "budgets:") {
		t.Fatalf("expected budgets in YAML, got:\n%s", data)
	}

	dir := t.TempDir()
	path := filepath.Clean(filepath.Join(dir, "fabrica.yaml"))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Cost.Budgets) != 2 {
		t.Fatalf("want 2 budgets, got %d", len(loaded.Cost.Budgets))
	}
	if loaded.Cost.Budgets[0].Scope != "total" || loaded.Cost.Budgets[0].Monthly != 400 {
		t.Fatalf("unexpected first budget: %+v", loaded.Cost.Budgets[0])
	}
}

// TestLoadInvalidYAML verifies error when YAML is malformed.
func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(":::invalid yaml content {{{"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
	if !strings.Contains(err.Error(), "parsing config") {
		t.Errorf("expected parsing error, got: %v", err)
	}
}

// TestLoadEmptyFile verifies empty file returns defaults.
func TestLoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error for empty file: %v", err)
	}
	// Empty file should still return defaults
	if cfg.Cloud.Provider != "aws" {
		t.Errorf("provider = %q, want aws", cfg.Cloud.Provider)
	}
}

// TestSaveAndLoadRoundTrip verifies Save then Load round-trips config data.
func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.yaml")

	cfg := Defaults()
	cfg.Cloud.AWS.Region = "eu-west-1"
	cfg.Cloud.AWS.AccountID = "987654321098"
	cfg.State.Bucket = "my-state-bucket"

	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Cloud.AWS.Region != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1", loaded.Cloud.AWS.Region)
	}
	if loaded.Cloud.AWS.AccountID != "987654321098" {
		t.Errorf("account = %q, want 987654321098", loaded.Cloud.AWS.AccountID)
	}
	if loaded.State.Bucket != "my-state-bucket" {
		t.Errorf("bucket = %q, want my-state-bucket", loaded.State.Bucket)
	}
}

// TestSaveEmptyPath uses default filename.
func TestSaveEmptyPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cfg := Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"

	if err := cfg.Save(""); err != nil {
		t.Fatalf("Save with empty path: %v", err)
	}

	// Verify the default file was created
	if _, err := os.Stat(DefaultFile); err != nil {
		t.Fatalf("expected default file %s to exist: %v", DefaultFile, err)
	}
}

// TestNormalizeDefaults verifies normalize fills in missing defaults.
func TestNormalizeDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.normalize()

	if cfg.Cloud.Provider != DefaultProvider {
		t.Errorf("provider = %q, want %s", cfg.Cloud.Provider, DefaultProvider)
	}
	if cfg.Cloud.AWS.Region != DefaultAWSRegion {
		t.Errorf("region = %q, want %s", cfg.Cloud.AWS.Region, DefaultAWSRegion)
	}
	if cfg.State.Table != DefaultStateTable {
		t.Errorf("table = %q, want %s", cfg.State.Table, DefaultStateTable)
	}
	if cfg.Cloud.AWS.Tags == nil {
		t.Error("expected non-nil tags map after normalize")
	}
}

// TestNormalizePreservesExistingValues verifies normalize does not overwrite set values.
func TestNormalizePreservesExistingValues(t *testing.T) {
	cfg := &Config{
		Cloud: Cloud{
			Provider: "aws",
			AWS: AWS{
				Region: "ap-southeast-1",
				Tags:   map[string]string{"env": "prod"},
			},
		},
		State: State{
			Table: "custom-lock-table",
		},
	}
	cfg.normalize()

	if cfg.Cloud.AWS.Region != "ap-southeast-1" {
		t.Errorf("region = %q, want ap-southeast-1 (should not be overwritten)", cfg.Cloud.AWS.Region)
	}
	if cfg.State.Table != "custom-lock-table" {
		t.Errorf("table = %q, want custom-lock-table (should not be overwritten)", cfg.State.Table)
	}
	if cfg.Cloud.AWS.Tags["env"] != "prod" {
		t.Errorf("tags.env = %q, want prod", cfg.Cloud.AWS.Tags["env"])
	}
}

// TestLoadProfileSpecific verifies loading a profile-specific config file.
func TestLoadProfileSpecific(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica-prod.yaml")
	content := `cloud:
  aws:
    region: us-west-2
    accountId: "999999999999"
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Cloud.AWS.Region != "us-west-2" {
		t.Errorf("region = %q, want us-west-2", cfg.Cloud.AWS.Region)
	}
	if cfg.Cloud.AWS.AccountID != "999999999999" {
		t.Errorf("accountID = %q, want 999999999999", cfg.Cloud.AWS.AccountID)
	}
}

// TestYAMLWithAllFields verifies YAML output includes all config sections.
func TestYAMLWithAllFields(t *testing.T) {
	cfg := Defaults()
	cfg.Cloud.AWS.Region = "us-east-1"
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Perforce.InstanceType = "m5.xlarge"
	cfg.Horde.InstanceType = "m7i.2xlarge"

	data, err := cfg.YAML()
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	text := string(data)
	for _, want := range []string{"cloud:", "perforce:", "horde:", "accountId:"} {
		if !strings.Contains(text, want) {
			t.Errorf("YAML missing %q:\n%s", want, text)
		}
	}
}

// TestSaveToReadOnlyPath verifies Save returns error when path is not writable.
func TestSaveToReadOnlyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly", "fabrica.yaml")

	cfg := Defaults()
	err := cfg.Save(path)
	if err == nil {
		t.Fatal("expected error when parent directory does not exist")
	}
	if !strings.Contains(err.Error(), "writing config") {
		t.Errorf("expected writing config error, got: %v", err)
	}
}

// TestLoadWithPartialDefaults verifies that loading a partial file still
// has default values for missing sections.
func TestLoadWithPartialDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.yaml")
	content := `cloud:
  aws:
    region: eu-central-1
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Cloud.AWS.Region != "eu-central-1" {
		t.Errorf("region = %q, want eu-central-1", cfg.Cloud.AWS.Region)
	}
	// Provider defaults preserved
	if cfg.Cloud.Provider != "aws" {
		t.Errorf("provider = %q, want aws (default)", cfg.Cloud.Provider)
	}
	// Tags should not be nil after Load (normalize is called)
	if cfg.Cloud.AWS.Tags == nil {
		t.Error("tags should not be nil after Load")
	}
}
