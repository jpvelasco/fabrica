package aws

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewProvider(t *testing.T) {
	cfg := config.Defaults()
	p, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	if p.Name() != "aws" {
		t.Errorf("Name() = %q, want aws", p.Name())
	}
	ap, ok := p.(*awsProvider)
	if !ok {
		t.Fatal("expected *awsProvider")
	}
	if ap.Resources() == nil {
		t.Fatal("Resources() returned nil")
	}
}

func TestNewProviderWithProfile(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.Profile = "my-profile"
	cfg.Cloud.AWS.Region = "eu-west-1"

	p, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	ap, ok := p.(*awsProvider)
	if !ok {
		t.Fatal("expected *awsProvider")
	}
	if ap.awsCfg.profile != "my-profile" {
		t.Errorf("profile = %q, want my-profile", ap.awsCfg.profile)
	}
	if ap.awsCfg.region != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1", ap.awsCfg.region)
	}
}

func TestProviderInterface(t *testing.T) {
	cfg := config.Defaults()
	p, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}

	// Verify type compliance
	var _ interface {
		Name() string
	} = p
}
