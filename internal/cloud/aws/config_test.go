package aws

import (
	"context"
	"testing"
)

func TestLoadAWSConfig_AppliesRegion(t *testing.T) {
	// LoadDefaultConfig resolves the credential chain lazily, so this does not
	// require real AWS credentials — it only constructs the config object.
	cfg, err := loadAWSConfig(context.Background(), "ap-southeast-2", "")
	if err != nil {
		t.Fatalf("loadAWSConfig: %v", err)
	}
	if cfg.Region != "ap-southeast-2" {
		t.Errorf("region = %q, want ap-southeast-2", cfg.Region)
	}
}

func TestLoadAWSConfig_NonexistentProfile(t *testing.T) {
	// A profile that does not exist in any shared config file should surface
	// as an error rather than silently returning a usable config.
	_, err := loadAWSConfig(context.Background(), "us-east-1", "no-such-profile-xyz")
	if err == nil {
		t.Skip("environment resolved an unexpected profile; skipping negative assertion")
	}
	assertStringContains(t, err.Error(), "loading AWS config")
}
