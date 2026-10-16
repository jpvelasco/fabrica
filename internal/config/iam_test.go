package config

import (
	"strings"
	"testing"
)

func TestResolveIAMModuleWins(t *testing.T) {
	global := IAM{Path: "/fabrica/", PermissionsBoundary: "arn:aws:iam::123456789012:policy/global"}
	module := IAM{Path: "/workstation/"}
	got, err := ResolveIAM(global, module)
	if err != nil {
		t.Fatalf("ResolveIAM: %v", err)
	}
	if got.Path != "/workstation/" {
		t.Errorf("path = %q, want /workstation/", got.Path)
	}
	if got.PermissionsBoundary != global.PermissionsBoundary {
		t.Errorf("boundary = %q, want the global ARN", got.PermissionsBoundary)
	}
}

func TestResolveIAMRejectsBadPathAndBoundary(t *testing.T) {
	if _, err := ResolveIAM(IAM{Path: "fabrica"}, IAM{}); err == nil || !strings.Contains(err.Error(), "must start and end with /") {
		t.Fatalf("path error = %v", err)
	}
	if _, err := ResolveIAM(IAM{Path: "/fabrica"}, IAM{}); err == nil {
		t.Fatal("path missing trailing slash was accepted")
	}
	if _, err := ResolveIAM(IAM{PermissionsBoundary: "boundary"}, IAM{}); err == nil || !strings.Contains(err.Error(), "IAM policy ARN") {
		t.Fatalf("boundary error = %v", err)
	}
	if _, err := ResolveIAM(IAM{Path: "/fabrica//roles/"}, IAM{}); err == nil {
		t.Fatal("empty path segment was accepted")
	}
	roleARN := "arn:aws:iam::123456789012:role/fabrica-workstation-role"
	if _, err := ResolveIAM(IAM{PermissionsBoundary: roleARN}, IAM{}); err == nil {
		t.Fatal("role ARN was accepted as a permissions boundary")
	}
	for _, arn := range []string{
		"arn:aws:iam::123456789012:policy/fabrica-boundary",
		"arn:aws-us-gov:iam::123456789012:policy/fabrica-boundary",
		"arn:aws-cn:iam::123456789012:policy/team/fabrica-boundary",
		"arn:aws:iam::aws:policy/ReadOnlyAccess",
	} {
		if _, err := ResolveIAM(IAM{PermissionsBoundary: arn}, IAM{}); err != nil {
			t.Errorf("policy ARN %s: %v", arn, err)
		}
	}
	if _, err := ResolveIAM(IAM{PermissionsBoundary: "arn:aws:iam::123456789012:policy/"}, IAM{}); err == nil {
		t.Fatal("empty policy name was accepted")
	}
	if _, err := ResolveIAM(IAM{}, IAM{PermissionsBoundary: "arn:aws-us-gov:iam::123456789012:policy/mod"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateIAM(t *testing.T) {
	cfg := Defaults()
	cfg.Cloud.AWS.IAM = IAM{Path: "/fabrica/", PermissionsBoundary: "arn:aws:iam::123456789012:policy/b"}
	cfg.Workstation.IAM.Path = "/workstation/"
	if err := cfg.ValidateIAM(); err != nil {
		t.Fatal(err)
	}
	cfg.Horde.IAM.Path = "horde"
	if err := cfg.ValidateIAM(); err == nil {
		t.Fatal("expected horde path to fail validation")
	}
	if err := (*Config)(nil).ValidateIAM(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadIAMAndVolumeRequestTags(t *testing.T) {
	path := writeConfig(t, `cloud:
  aws:
    iam:
      path: /fabrica/
      permissionsBoundary: arn:aws:iam::123456789012:policy/fabrica-boundary
    volumeRequestTags:
      Project: fabrica-e2e
workstation:
  iam:
    path: /workstation/
`)
	cfg := loadConfig(t, path)
	if cfg.Cloud.AWS.IAM.Path != "/fabrica/" {
		t.Errorf("global path = %q", cfg.Cloud.AWS.IAM.Path)
	}
	if cfg.Cloud.AWS.VolumeRequestTags["Project"] != "fabrica-e2e" {
		t.Errorf("volume tags = %v", cfg.Cloud.AWS.VolumeRequestTags)
	}
	if cfg.Workstation.IAM.Path != "/workstation/" {
		t.Errorf("module path = %q", cfg.Workstation.IAM.Path)
	}
	clone := cfg.Clone()
	clone.Cloud.AWS.VolumeRequestTags["Project"] = "changed"
	if cfg.Cloud.AWS.VolumeRequestTags["Project"] != "fabrica-e2e" {
		t.Fatal("clone mutated the original volume request tags")
	}
}

func TestRestoreVolumeRequestTagCaseEdges(t *testing.T) {
	cfg := Defaults()
	if err := restoreVolumeRequestTagCase("unused", nil); err != nil {
		t.Fatal(err)
	}
	if err := restoreVolumeRequestTagCase("", cfg); err != nil {
		t.Fatal(err)
	}
	if err := restoreVolumeRequestTagCase(t.TempDir(), cfg); err == nil {
		t.Fatal("expected a directory read to fail")
	}
	if err := restoreVolumeRequestTagCase(t.TempDir()+"/missing.yaml", cfg); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, "cloud: [\n")
	if err := restoreVolumeRequestTagCase(path, cfg); err == nil {
		t.Fatal("expected a YAML parse error")
	}
}
