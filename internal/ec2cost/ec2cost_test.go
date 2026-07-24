package ec2cost

import (
	"fmt"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/cost"
)

func TestInstanceAndVolume(t *testing.T) {
	resources := InstanceAndVolume("m7i.xlarge", 100)

	if len(resources) != 2 {
		t.Fatalf("got %d resources, want 2", len(resources))
	}

	want := []cost.Resource{
		{TypeName: cloud.TypeAWSEC2Instance, Name: "m7i.xlarge"},
		{TypeName: cloud.TypeAWSEC2Volume, Name: "gp3-100GiB"},
	}
	for i, r := range resources {
		if r != want[i] {
			t.Errorf("resource[%d] = %+v, want %+v", i, r, want[i])
		}
	}
}

func TestInstanceAndVolume_VariousTypes(t *testing.T) {
	cases := []struct {
		instanceType string
		volumeSize   int
	}{
		{"m5.xlarge", 500},
		{"g4dn.xlarge", 200},
		{"c7i.xlarge", 100},
		{"m7i.2xlarge", 1000},
	}

	for _, tc := range cases {
		t.Run(tc.instanceType+"-vol"+fmt.Sprint(tc.volumeSize), func(t *testing.T) {
			res := InstanceAndVolume(tc.instanceType, tc.volumeSize)
			if len(res) != 2 {
				t.Fatalf("got %d resources, want 2", len(res))
			}
			if res[0].TypeName != cloud.TypeAWSEC2Instance {
				t.Errorf("instance type name = %q, want %q", res[0].TypeName, cloud.TypeAWSEC2Instance)
			}
			if res[0].Name != tc.instanceType {
				t.Errorf("instance name = %q, want %q", res[0].Name, tc.instanceType)
			}
			if res[1].TypeName != cloud.TypeAWSEC2Volume {
				t.Errorf("volume type name = %q, want %q", res[1].TypeName, cloud.TypeAWSEC2Volume)
			}
			wantVolName := fmt.Sprintf("gp3-%dGiB", tc.volumeSize)
			if res[1].Name != wantVolName {
				t.Errorf("volume name = %q, want %q", res[1].Name, wantVolName)
			}
		})
	}
}

func TestInstanceAndVolume_TypeNamesMatchCloudConstants(t *testing.T) {
	res := InstanceAndVolume("m5.large", 50)
	if res[0].TypeName != cloud.TypeAWSEC2Instance {
		t.Errorf("instance TypeName = %q, want cloud constant %q", res[0].TypeName, cloud.TypeAWSEC2Instance)
	}
	if res[1].TypeName != cloud.TypeAWSEC2Volume {
		t.Errorf("volume TypeName = %q, want cloud constant %q", res[1].TypeName, cloud.TypeAWSEC2Volume)
	}
	// Verify the constants match the expected strings
	if cloud.TypeAWSEC2Instance != "AWS::EC2::Instance" {
		t.Errorf("cloud.TypeAWSEC2Instance = %q, want %q", cloud.TypeAWSEC2Instance, "AWS::EC2::Instance")
	}
	if cloud.TypeAWSEC2Volume != "AWS::EC2::Volume" {
		t.Errorf("cloud.TypeAWSEC2Volume = %q, want %q", cloud.TypeAWSEC2Volume, "AWS::EC2::Volume")
	}
}
