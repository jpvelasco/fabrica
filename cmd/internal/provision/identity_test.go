package provision

import (
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

type testIdentityProvider struct {
	account string
	region  string
	err     error
}

func (p testIdentityProvider) Name() string { return "test" }
func (p testIdentityProvider) Identity(ctx context.Context) (string, string, string, error) {
	return p.account, "", p.region, p.err
}
func (p testIdentityProvider) Resources() cloud.ResourceClient { return nil }

func TestResolveIdentity_Success(t *testing.T) {
	p := testIdentityProvider{account: "123456789012", region: "us-east-1"}
	account, region, err := ResolveIdentity(context.Background(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if account != "123456789012" {
		t.Errorf("account = %q, want %q", account, "123456789012")
	}
	if region != "us-east-1" {
		t.Errorf("region = %q, want %q", region, "us-east-1")
	}
}

func TestResolveIdentity_Error(t *testing.T) {
	p := testIdentityProvider{err: errors.New("credentials invalid")}
	account, region, err := ResolveIdentity(context.Background(), p)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if account != "" {
		t.Errorf("account = %q, want empty on error", account)
	}
	if region != "" {
		t.Errorf("region = %q, want empty on error", region)
	}
}

func TestResolveIdentity_NilProvider(t *testing.T) {
	account, region, err := ResolveIdentity(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "no provider configured; run 'fabrica setup' first" {
		t.Fatalf("error = %q, want missing-provider guidance", got)
	}
	if account != "" || region != "" {
		t.Fatalf("identity = (%q, %q), want empty values", account, region)
	}
}
