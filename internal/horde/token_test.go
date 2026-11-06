package horde

import (
	"strings"
	"testing"
)

func TestServiceTokenParameterFor(t *testing.T) {
	nonce, err := NewServiceTokenNonce()
	if err != nil {
		t.Fatalf("NewServiceTokenNonce: %v", err)
	}
	name, err := ServiceTokenParameterFor(nonce)
	if err != nil {
		t.Fatalf("ServiceTokenParameterFor: %v", err)
	}
	if name != "/fabrica/horde/"+nonce+"/service-token" {
		t.Errorf("name = %q", name)
	}
	if !IsServiceTokenName(name) {
		t.Errorf("IsServiceTokenName(%q) = false", name)
	}
}

func TestServiceTokenParameterForRejectsUnsafeNonce(t *testing.T) {
	for _, nonce := range []string{"", "abc", "../../etc", strings.Repeat("A", 32), strings.Repeat("a", 31) + "/", "x; rm -rf /"} {
		if _, err := ServiceTokenParameterFor(nonce); err == nil {
			t.Errorf("ServiceTokenParameterFor(%q) accepted an unsafe nonce", nonce)
		}
	}
}

func TestServiceTokenParameterARN(t *testing.T) {
	name := "/fabrica/horde/" + strings.Repeat("a", 32) + "/service-token"
	arn, err := ServiceTokenParameterARN("us-west-2", "123456789012", name)
	if err != nil {
		t.Fatalf("ServiceTokenParameterARN: %v", err)
	}
	if arn != "arn:aws:ssm:us-west-2:123456789012:parameter"+name {
		t.Errorf("arn = %q", arn)
	}
	if _, err := ServiceTokenParameterARN("", "123456789012", name); err == nil {
		t.Error("empty region accepted")
	}
	if _, err := ServiceTokenParameterARN("us-west-2", "123456789012", "/fabrica/horde/*"); err == nil {
		t.Error("wildcard name accepted")
	}
}
