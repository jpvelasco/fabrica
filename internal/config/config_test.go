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

func TestSaveWritesSchemaNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")

	cfg := Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.State.KMSKeyID = "alias/fabrica"

	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := os.ReadFile(path)
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
	v.ReadConfig(strings.NewReader(yaml))
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
