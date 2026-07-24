package ec2cost

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/cost"
)

func TestInstanceAndVolume(t *testing.T) {
	resources := InstanceAndVolume("m7i.xlarge", 100)

	if len(resources) != 2 {
		t.Fatalf("got %d resources, want 2", len(resources))
	}

	want := []cost.Resource{
		{TypeName: "AWS::EC2::Instance", Name: "m7i.xlarge"},
		{TypeName: "AWS::EC2::Volume", Name: "gp3-100GiB"},
	}
	for i, r := range resources {
		if r != want[i] {
			t.Errorf("resource[%d] = %+v, want %+v", i, r, want[i])
		}
	}
}
