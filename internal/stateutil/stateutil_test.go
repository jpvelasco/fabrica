package stateutil_test

import (
	"testing"

	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
)

func module(resources ...fabricastate.ModuleResource) *fabricastate.ModuleState {
	return &fabricastate.ModuleState{Resources: resources}
}

func TestResourceByType_Found(t *testing.T) {
	m := module(
		fabricastate.ModuleResource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc"},
		fabricastate.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-xyz"},
	)

	r, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !ok {
		t.Fatal("expected resource to be found")
	}
	if r.Identifier != "i-xyz" {
		t.Errorf("got identifier %q, want %q", r.Identifier, "i-xyz")
	}
}

func TestResourceByType_NotFound(t *testing.T) {
	m := module(
		fabricastate.ModuleResource{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc"},
	)

	_, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if ok {
		t.Fatal("expected resource not to be found")
	}
}

func TestResourceByType_Empty(t *testing.T) {
	m := module()
	_, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if ok {
		t.Fatal("expected false for empty resource list")
	}
}

func TestResourceByType_ReturnsFirst(t *testing.T) {
	m := module(
		fabricastate.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-first"},
		fabricastate.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-second"},
	)

	r, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !ok {
		t.Fatal("expected resource to be found")
	}
	if r.Identifier != "i-first" {
		t.Errorf("got %q, want first entry", r.Identifier)
	}
}
