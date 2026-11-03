package config

import (
	"errors"
	"path/filepath"
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
    tags:
      Project: fabrica-e2e
      Env: prod
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
	if cfg.Cloud.AWS.Tags["Project"] != "fabrica-e2e" {
		t.Errorf("tags[Project] = %q, want fabrica-e2e (tags: %v)", cfg.Cloud.AWS.Tags["Project"], cfg.Cloud.AWS.Tags)
	}
	if cfg.Cloud.AWS.Tags["Env"] != "prod" {
		t.Errorf("tags[Env] = %q, want prod (tags: %v)", cfg.Cloud.AWS.Tags["Env"], cfg.Cloud.AWS.Tags)
	}
	if _, lowered := cfg.Cloud.AWS.Tags["project"]; lowered {
		t.Errorf("tags leaked a lowercased key: %v", cfg.Cloud.AWS.Tags)
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

// TestAWSTagsSaveLoadRoundTrip verifies a mixed-case operator tag set
// survives the config save/load round trip with its exact key casing.
func TestAWSTagsSaveLoadRoundTrip(t *testing.T) {
	cfg := Defaults()
	cfg.Cloud.AWS.Tags = map[string]string{"Project": "fabrica-e2e", "Env": "prod"}
	cfg.Cloud.AWS.VolumeRequestTags = map[string]string{"CostCenter": "studio"}
	path := filepath.Join(t.TempDir(), "fabrica.yaml")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := loadConfig(t, path)
	if got.Cloud.AWS.Tags["Project"] != "fabrica-e2e" {
		t.Errorf("round-trip tags[Project] = %q, want fabrica-e2e (tags: %v)", got.Cloud.AWS.Tags["Project"], got.Cloud.AWS.Tags)
	}
	if got.Cloud.AWS.Tags["Env"] != "prod" {
		t.Errorf("round-trip tags[Env] = %q, want prod (tags: %v)", got.Cloud.AWS.Tags["Env"], got.Cloud.AWS.Tags)
	}
	if got.Cloud.AWS.VolumeRequestTags["CostCenter"] != "studio" {
		t.Errorf("round-trip volume tags = %v", got.Cloud.AWS.VolumeRequestTags)
	}
}

// TestLoadAWSTagMapsReadFailure forces the re-read inside Load to fail,
// covering the error branch Viper's successful first read makes unreachable.
func TestLoadAWSTagMapsReadFailure(t *testing.T) {
	path := writeConfig(t, "cloud:\n  aws:\n    tags:\n      Project: studio\n")
	orig := readConfigFile
	readConfigFile = func(string) ([]byte, error) {
		return nil, errors.New("disk failure")
	}
	t.Cleanup(func() { readConfigFile = orig })

	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "reading AWS tag maps") {
		t.Fatalf("Load = %v, want wrapped 'reading AWS tag maps' error", err)
	}
}

func TestRestoreAWSTagMapsEdges(t *testing.T) {
	cfg := Defaults()
	if err := restoreAWSTagMaps("unused", nil); err != nil {
		t.Fatal(err)
	}
	if err := restoreAWSTagMaps("", cfg); err != nil {
		t.Fatal(err)
	}
	if err := restoreAWSTagMaps(t.TempDir(), cfg); err == nil {
		t.Fatal("expected a directory read to fail")
	}
	if err := restoreAWSTagMaps(t.TempDir()+"/missing.yaml", cfg); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, "cloud: [\n")
	if err := restoreAWSTagMaps(path, cfg); err == nil {
		t.Fatal("expected a YAML parse error")
	}

	// A file with only one of the two maps restores that map and leaves the
	// other in place.
	before := map[string]string{"Existing": "kept"}
	cfg = Defaults()
	cfg.Cloud.AWS.Tags = before
	path = writeConfig(t, "cloud:\n  aws:\n    volumeRequestTags:\n      Project: restored\n")
	if err := restoreAWSTagMaps(path, cfg); err != nil {
		t.Fatalf("restoreAWSTagMaps: %v", err)
	}
	if cfg.Cloud.AWS.VolumeRequestTags["Project"] != "restored" {
		t.Errorf("volume tags = %v, want Project restored", cfg.Cloud.AWS.VolumeRequestTags)
	}
	if cfg.Cloud.AWS.Tags["Existing"] != "kept" {
		t.Errorf("tags = %v, want the in-memory map left in place", cfg.Cloud.AWS.Tags)
	}
}
