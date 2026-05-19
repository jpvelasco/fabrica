package perforce

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/cost"
)

func TestEC2InstanceEstimator_KnownType(t *testing.T) {
	e := ec2InstanceEstimator{}
	got, err := e.Estimate(cost.Resource{TypeName: TypeAWSEC2Instance, Name: "m5.xlarge"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := 0.192 * hoursPerMonth
	if got.Amount != want {
		t.Errorf("m5.xlarge: Amount = %.2f, want %.2f", got.Amount, want)
	}
	if got.Confidence != cost.High {
		t.Errorf("Confidence = %v, want High", got.Confidence)
	}
}

func TestEC2InstanceEstimator_UnknownType(t *testing.T) {
	e := ec2InstanceEstimator{}
	_, err := e.Estimate(cost.Resource{TypeName: TypeAWSEC2Instance, Name: "z99.ultraxl"})
	if err == nil {
		t.Error("expected error for unknown instance type")
	}
}

func TestEC2VolumeEstimator_500GiB(t *testing.T) {
	e := ec2VolumeEstimator{}
	got, err := e.Estimate(cost.Resource{TypeName: TypeAWSEC2Volume, Name: "gp3-500GiB"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := 500.0 * gp3PricePerGiB
	if got.Amount != want {
		t.Errorf("500 GiB gp3: Amount = %.2f, want %.2f", got.Amount, want)
	}
	if got.Confidence != cost.High {
		t.Errorf("Confidence = %v, want High", got.Confidence)
	}
}

func TestEC2VolumeEstimator_InvalidName(t *testing.T) {
	e := ec2VolumeEstimator{}
	_, err := e.Estimate(cost.Resource{TypeName: TypeAWSEC2Volume, Name: "unknown"})
	if err == nil {
		t.Error("expected error for unparseable volume name")
	}
}

func TestCostRegistryDuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	// init() already registered these; registering again must panic.
	cost.Global.Register(TypeAWSEC2Instance, ec2InstanceEstimator{})
}
