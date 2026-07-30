package ec2plan

import (
	"fmt"
	"testing"
)

// fakeT collects errors instead of failing the test, so we can assert the
// number of errors reported by the assertion helpers.
type fakeT struct {
	errors []string
}

func (f *fakeT) Helper() {}

func (f *fakeT) Errorf(format string, args ...any) {
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}

func (f *fakeT) Error(args ...any) {
	f.errors = append(f.errors, fmt.Sprint(args...))
}

func (f *fakeT) numErrors() int {
	return len(f.errors)
}

func TestCheckStrMatch(t *testing.T) {
	ft := &fakeT{}
	checkStr(ft, "hello", "hello", "Name")
	if ft.numErrors() != 0 {
		t.Errorf("expected 0 errors, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestCheckStrMismatch(t *testing.T) {
	ft := &fakeT{}
	checkStr(ft, "hello", "world", "Name")
	if ft.numErrors() != 1 {
		t.Errorf("expected 1 error, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestCheckStrEmptyWant(t *testing.T) {
	ft := &fakeT{}
	checkStr(ft, "anything", "", "Name")
	if ft.numErrors() != 0 {
		t.Errorf("expected 0 errors for empty want, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestCheckIntMatch(t *testing.T) {
	ft := &fakeT{}
	checkInt(ft, 100, 100, "VolumeSize")
	if ft.numErrors() != 0 {
		t.Errorf("expected 0 errors, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestCheckIntMismatch(t *testing.T) {
	ft := &fakeT{}
	checkInt(ft, 100, 200, "VolumeSize")
	if ft.numErrors() != 1 {
		t.Errorf("expected 1 error, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestCheckIntZeroWant(t *testing.T) {
	ft := &fakeT{}
	checkInt(ft, 999, 0, "VolumeSize")
	if ft.numErrors() != 0 {
		t.Errorf("expected 0 errors for zero want, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestAssertBaseAllMatch(t *testing.T) {
	ft := &fakeT{}
	got := Base{
		Account:      "123456789012",
		Region:       "us-east-1",
		InstanceType: "m7i.2xlarge",
		VolumeSize:   100,
		AllowedCIDR:  "10.0.0.0/16",
		SGName:       "fabrica-horde-sg",
		InstanceName: "fabrica-horde",
	}
	want := Base{
		Account:      "123456789012",
		Region:       "us-east-1",
		InstanceType: "m7i.2xlarge",
		VolumeSize:   100,
		AllowedCIDR:  "10.0.0.0/16",
		SGName:       "fabrica-horde-sg",
		InstanceName: "fabrica-horde",
	}
	AssertBase(ft, got, want)
	if ft.numErrors() != 0 {
		t.Errorf("expected 0 errors, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestAssertBaseSubset(t *testing.T) {
	ft := &fakeT{}
	got := Base{
		Account:      "123456789012",
		Region:       "us-east-1",
		InstanceType: "m7i.2xlarge",
		VolumeSize:   100,
	}
	want := Base{InstanceType: "m7i.2xlarge"}
	AssertBase(ft, got, want)
	if ft.numErrors() != 0 {
		t.Errorf("expected 0 errors for subset check, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestAssertBaseMismatch(t *testing.T) {
	ft := &fakeT{}
	got := Base{
		Account:      "123456789012",
		InstanceType: "t3.micro",
	}
	want := Base{
		InstanceType: "m7i.2xlarge",
		VolumeSize:   100,
	}
	AssertBase(ft, got, want)
	if ft.numErrors() != 2 {
		t.Errorf("expected 2 errors (InstanceType + VolumeSize), got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestAssertVPCMatch(t *testing.T) {
	ft := &fakeT{}
	got := Base{VPCID: "vpc-123", SubnetID: "subnet-456"}
	AssertVPC(ft, got, "vpc-123", "subnet-456")
	if ft.numErrors() != 0 {
		t.Errorf("expected 0 errors, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestAssertVPCMismatch(t *testing.T) {
	ft := &fakeT{}
	got := Base{VPCID: "vpc-123", SubnetID: "subnet-456"}
	AssertVPC(ft, got, "vpc-999", "subnet-999")
	if ft.numErrors() != 2 {
		t.Errorf("expected 2 errors, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestAssertVPCResolved(t *testing.T) {
	ft := &fakeT{}
	got := Base{VPCID: "vpc-123", SubnetID: "subnet-456", DefaultVPC: true}
	AssertVPCResolved(ft, got, "vpc-123", "subnet-456")
	if ft.numErrors() != 0 {
		t.Errorf("expected 0 errors, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestAssertVPCResolvedNotDefault(t *testing.T) {
	ft := &fakeT{}
	got := Base{VPCID: "vpc-123", SubnetID: "subnet-456", DefaultVPC: false}
	AssertVPCResolved(ft, got, "vpc-123", "subnet-456")
	if ft.numErrors() != 1 {
		t.Errorf("expected 1 error (DefaultVPC=false), got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestAssertVPCExplicit(t *testing.T) {
	ft := &fakeT{}
	got := Base{VPCID: "vpc-123", SubnetID: "subnet-456", DefaultVPC: false}
	AssertVPCExplicit(ft, got, "vpc-123", "subnet-456")
	if ft.numErrors() != 0 {
		t.Errorf("expected 0 errors, got %d: %v", ft.numErrors(), ft.errors)
	}
}

func TestAssertVPCExplicitIsDefault(t *testing.T) {
	ft := &fakeT{}
	got := Base{VPCID: "vpc-123", SubnetID: "subnet-456", DefaultVPC: true}
	AssertVPCExplicit(ft, got, "vpc-123", "subnet-456")
	if ft.numErrors() != 1 {
		t.Errorf("expected 1 error (DefaultVPC=true), got %d: %v", ft.numErrors(), ft.errors)
	}
}
