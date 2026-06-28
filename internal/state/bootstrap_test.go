package state

import (
	"context"
	"errors"
	"testing"

	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

type fakeBootProvider struct {
	account     string
	identityErr error
	bucketRes   fabricac.StateBackendCreateResult
	bucketErr   error
	tableRes    fabricac.StateBackendCreateResult
	tableErr    error
	tableCalled bool
}

func (f *fakeBootProvider) Name() string { return "fake" }
func (f *fakeBootProvider) Identity(ctx context.Context) (string, string, string, error) {
	return f.account, "arn", "us-west-2", f.identityErr
}
func (f *fakeBootProvider) Resources() fabricac.ResourceClient { return nil }
func (f *fakeBootProvider) EnsureStateBucket(ctx context.Context, bucket, region string) (fabricac.StateBackendCreateResult, error) {
	return f.bucketRes, f.bucketErr
}
func (f *fakeBootProvider) EnsureStateLockTable(ctx context.Context, table string) (fabricac.StateBackendCreateResult, error) {
	f.tableCalled = true
	return f.tableRes, f.tableErr
}

// plainProvider implements only cloud.Provider — not StateBackendBootstrapper.
type plainProvider struct{ account string }

func (plainProvider) Name() string { return "plain" }
func (p plainProvider) Identity(ctx context.Context) (string, string, string, error) {
	return p.account, "arn", "us-west-2", nil
}
func (plainProvider) Resources() fabricac.ResourceClient { return nil }

func cfgFor(account string) *config.Config {
	c := config.Defaults()
	c.Cloud.AWS.AccountID = account
	return c
}

func TestBootstrapCreatesBoth(t *testing.T) {
	p := &fakeBootProvider{
		account:   "123",
		bucketRes: fabricac.StateBackendCreateResult{Identifier: "fabrica-state-123", Created: true},
		tableRes:  fabricac.StateBackendCreateResult{Identifier: "fabrica-state-lock", Created: true},
	}
	results, err := Bootstrap(context.Background(), p, cfgFor("123"))
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Existed || results[1].Existed {
		t.Errorf("both should be newly created: %+v", results)
	}
}

func TestBootstrapIdempotent(t *testing.T) {
	p := &fakeBootProvider{
		account:   "123",
		bucketRes: fabricac.StateBackendCreateResult{Identifier: "b", Created: false},
		tableRes:  fabricac.StateBackendCreateResult{Identifier: "t", Created: false},
	}
	results, err := Bootstrap(context.Background(), p, cfgFor("123"))
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if !results[0].Existed || !results[1].Existed {
		t.Errorf("both should report Existed: %+v", results)
	}
}

func TestBootstrapBucketFailureSkipsTable(t *testing.T) {
	p := &fakeBootProvider{account: "123", bucketErr: errors.New("boom")}
	_, err := Bootstrap(context.Background(), p, cfgFor("123"))
	if err == nil {
		t.Fatal("expected error")
	}
	if p.tableCalled {
		t.Error("table creation should not be attempted after bucket failure")
	}
}

func TestBootstrapIdentityFailureFirst(t *testing.T) {
	p := &fakeBootProvider{identityErr: errors.New("no creds")}
	_, err := Bootstrap(context.Background(), p, cfgFor("123"))
	if err == nil {
		t.Fatal("expected identity error")
	}
}

func TestBootstrapUnsupportedProvider(t *testing.T) {
	_, err := Bootstrap(context.Background(), plainProvider{account: "123"}, cfgFor("123"))
	if err == nil {
		t.Fatal("expected unsupported-provider error")
	}
}
