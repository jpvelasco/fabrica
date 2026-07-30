package userdata

import (
	"errors"
	"testing"
)

func TestPrepare_NoHooks(t *testing.T) {
	err := Prepare(nil, nil)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestPrepare_ApplyOnly(t *testing.T) {
	var called bool
	err := Prepare(func() { called = true }, nil)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if !called {
		t.Fatal("apply hook was not called")
	}
}

func TestPrepare_ValidateOnly(t *testing.T) {
	var called bool
	err := Prepare(nil, func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if !called {
		t.Fatal("validate hook was not called")
	}
}

func TestPrepare_ApplyThenValidate(t *testing.T) {
	var order []string
	err := Prepare(
		func() { order = append(order, "apply") },
		func() error {
			order = append(order, "validate")
			return nil
		},
	)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if len(order) != 2 || order[0] != "apply" || order[1] != "validate" {
		t.Fatalf("expected [apply validate], got %v", order)
	}
}

func TestPrepare_ValidateError(t *testing.T) {
	want := errors.New("required field missing")
	err := Prepare(nil, func() error { return want })
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != want {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

func TestPrepare_ApplyRunsEvenWhenValidateFails(t *testing.T) {
	var applyCalled bool
	err := Prepare(
		func() { applyCalled = true },
		func() error { return errors.New("validate error") },
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !applyCalled {
		t.Fatal("apply should run even when validate fails")
	}
}
