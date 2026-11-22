package provision

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

const abandonedName = "/fabrica/perforce/0123456789abcdef0123456789abcdef/admin-password"

func TestParameterSeamsWireParameterStore(t *testing.T) {
	store := &testutil.TestProvider{}
	put := ParameterPut(store)
	del := ParameterDelete(store)
	if put == nil || del == nil {
		t.Fatal("a ParameterStore provider must yield both seams")
	}
	if err := put(context.Background(), cloud.SecureParameter{Name: abandonedName, Value: "v"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := del(context.Background(), abandonedName); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(store.PutParameters) != 1 || len(store.DeletedParameters) != 1 {
		t.Fatalf("seams did not reach the store: puts=%d deletes=%d", len(store.PutParameters), len(store.DeletedParameters))
	}
}

func TestParameterSeamsNilWithoutParameterStore(t *testing.T) {
	// A provider with no SecureString support: only the resource client is
	// wired, so the type assertion fails and both seams are nil.
	// Embedding the interface promotes Provider's methods but not the
	// SecureString ones, so the type assertion to ParameterStore fails.
	p := &struct{ cloud.Provider }{}
	if ParameterPut(p) != nil || ParameterDelete(p) != nil {
		t.Fatal("a provider without ParameterStore must yield nil seams")
	}
}

func TestAbandonSecureParameterReturnsOriginalError(t *testing.T) {
	cause := errors.New("creating security group: denied")
	var deleted []string
	del := func(_ context.Context, name string) error {
		deleted = append(deleted, name)
		return nil
	}
	got := AbandonSecureParameter(context.Background(), del, abandonedName, cause)
	if got != cause {
		t.Fatalf("returned %v, want the original error", got)
	}
	if len(deleted) != 1 || deleted[0] != abandonedName {
		t.Fatalf("deleted = %v, want exactly the abandoned name", deleted)
	}
}

func TestAbandonSecureParameterAppendsDeleteFailure(t *testing.T) {
	cause := errors.New("creating IAM role: denied")
	del := func(context.Context, string) error { return errors.New("ssm throttled") }
	got := AbandonSecureParameter(context.Background(), del, abandonedName, cause)
	for _, want := range []string{"creating IAM role: denied", "also failed to delete SSM parameter " + abandonedName, "ssm throttled"} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("error %q missing %q", got.Error(), want)
		}
	}
}

func TestAbandonSecureParameterWithoutStoreSaysParameterSurvives(t *testing.T) {
	cause := errors.New("creating EC2 instance: quota")
	got := AbandonSecureParameter(context.Background(), nil, abandonedName, cause)
	if !strings.Contains(got.Error(), "creating EC2 instance: quota") || !strings.Contains(got.Error(), "was not deleted") || !strings.Contains(got.Error(), abandonedName) {
		t.Fatalf("error %q must keep the cause and name the surviving parameter", got.Error())
	}
}
