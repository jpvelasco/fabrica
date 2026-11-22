package teardown

import (
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

func wellFormedTestName(name string) bool { return name == "/fabrica/x/ok" }

func TestWireParameterDeleteRoutesParameterToStore(t *testing.T) {
	store := &testutil.TestProvider{}
	tc := &Command{}
	WireParameterDelete(tc, globals.Runtime{Provider: store})
	if tc.SDKDeleteFunc == nil {
		t.Fatal("WireParameterDelete must install SDKDeleteFunc for a ParameterStore provider")
	}
	if err := tc.SDKDeleteFunc(context.Background(), cloud.TypeAWSSSMParameter, "/fabrica/x/ok"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(store.DeletedParameters) != 1 || store.DeletedParameters[0] != "/fabrica/x/ok" {
		t.Fatalf("DeletedParameters = %v, want [/fabrica/x/ok]", store.DeletedParameters)
	}
}

func TestWireParameterDeleteFallsThroughForOtherTypes(t *testing.T) {
	tc := &Command{}
	WireParameterDelete(tc, globals.Runtime{Provider: &testutil.TestProvider{}})
	err := tc.SDKDeleteFunc(context.Background(), cloud.TypeAWSEC2Instance, "i-1")
	if !errors.Is(err, cloud.ErrNotHandled) {
		t.Fatalf("non-parameter type must return ErrNotHandled, got %v", err)
	}
}

func TestWireParameterDeleteNoopWithoutStore(t *testing.T) {
	tc := &Command{}
	WireParameterDelete(tc, globals.Runtime{Provider: nil})
	if tc.SDKDeleteFunc != nil {
		t.Error("a nil provider must leave SDKDeleteFunc unset")
	}
	WireParameterDelete(tc, globals.Runtime{Provider: &fakeProviderWithRC{rc: &fakeResourceClient{}}})
	if tc.SDKDeleteFunc != nil {
		t.Error("a provider without ParameterStore must leave SDKDeleteFunc unset")
	}
}

func TestRecordedParameter(t *testing.T) {
	r, ok := RecordedParameter("perforce", "/fabrica/x/ok", wellFormedTestName)
	if !ok {
		t.Fatal("a well-formed recorded name must yield a resource")
	}
	if r.TypeName != cloud.TypeAWSSSMParameter || r.Identifier != "/fabrica/x/ok" {
		t.Fatalf("resource = %+v", r)
	}
}

func TestRecordedParameterSkipsAbsentAndMalformedNames(t *testing.T) {
	if _, ok := RecordedParameter("perforce", "", wellFormedTestName); ok {
		t.Error("an absent name (pre-create or pre-#700 state) must be skipped silently")
	}
	if _, ok := RecordedParameter("perforce", "/fabrica/other", wellFormedTestName); ok {
		t.Error("a malformed recorded name must not widen the delete target")
	}
}
