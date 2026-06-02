package perforce

import (
	"fmt"
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

func TestEC2InstanceEstimator_AllKnownTypes(t *testing.T) {
	cases := []struct {
		name   string
		hourly float64
	}{
		{"m5.large", 0.096},
		{"m5.2xlarge", 0.384},
		{"c5.xlarge", 0.170},
		{"c5.2xlarge", 0.340},
		{"r5.xlarge", 0.252},
		{"m7i.xlarge", 0.2016},
		{"m7i.2xlarge", 0.4032},
		{"m7i.4xlarge", 0.8064},
		{"m7i.large", 0.1008},
	}
	e := ec2InstanceEstimator{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.Estimate(cost.Resource{TypeName: TypeAWSEC2Instance, Name: tc.name})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := tc.hourly * hoursPerMonth
			if !almostEqual(got.Amount, want) {
				t.Errorf("Amount = %.4f, want %.4f", got.Amount, want)
			}
			if got.Confidence != cost.High {
				t.Errorf("Confidence = %v, want High", got.Confidence)
			}
		})
	}
}

func almostEqual(a, b float64) bool {
	const eps = 1e-9
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < eps
}

func TestEC2VolumeEstimator_VariousSizes(t *testing.T) {
	cases := []struct{ gib int }{
		{100}, {250}, {500}, {1000}, {2000},
	}
	e := ec2VolumeEstimator{}
	for _, tc := range cases {
		name := fmt.Sprintf("gp3-%dGiB", tc.gib)
		t.Run(name, func(t *testing.T) {
			got, err := e.Estimate(cost.Resource{TypeName: TypeAWSEC2Volume, Name: name})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			want := float64(tc.gib) * gp3PricePerGiB
			if got.Amount != want {
				t.Errorf("Amount = %.4f, want %.4f", got.Amount, want)
			}
		})
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

func TestGPUInstancePrices(t *testing.T) {
	for _, tc := range []struct {
		typ    string
		hourly float64
	}{
		{"g4dn.xlarge", 0.526},
		{"g4dn.2xlarge", 0.752},
		{"g5.xlarge", 1.006},
		{"g5.2xlarge", 1.212},
	} {
		r := cost.Resource{TypeName: TypeAWSEC2Instance, Name: tc.typ}
		got, err := cost.Global.Estimate(TypeAWSEC2Instance, r)
		if err != nil {
			t.Errorf("%s: %v", tc.typ, err)
			continue
		}
		want := tc.hourly * hoursPerMonth
		if !almostEqual(got.Amount, want) {
			t.Errorf("%s: amount = %.4f, want %.4f", tc.typ, got.Amount, want)
		}
	}
}
