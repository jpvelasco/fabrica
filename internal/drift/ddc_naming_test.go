package drift

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/jpvelasco/fabrica/internal/state"
)

// Coordinator and Scylla rebuilds name the instance from the prefix. The
// Scylla cluster name is in-application and stays fixed.
func TestRebuildDDCFollowsPrefix(t *testing.T) {
	cases := []struct {
		name, role, instanceName string
	}{
		{"coordinator", "", "acme-ddc"},
		{"scylla", ddc.RoleScylla, "acme-ddc-scylla"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.DDC.ScyllaAmiID = "ami-scylla"
			st := state.NewState("123456789012", "us-east-1")
			props := map[string]string{"rootDevice": "/dev/sda1"}
			if tc.role != "" {
				props["role"] = tc.role
			}
			mod := &state.ModuleState{
				Name:    "ddc",
				Version: "ami-ddc",
				Resources: []state.ModuleResource{
					{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-ddc"},
					{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "acme-ddc-profile"},
					{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ddc", Properties: props},
				},
			}
			ds, err := rebuildInstanceDesiredState(context.Background(), acmePrefix, &mod.Resources[2], mod, st, cfg, nil)
			if err != nil {
				t.Fatalf("rebuild instance: %v", err)
			}
			if !strings.Contains(string(ds), `"Value":"`+tc.instanceName+`"`) {
				t.Errorf("rebuilt instance Name tag is not %s: %s", tc.instanceName, ds)
			}
			if tc.role == "" {
				if ud := decodedUserData(t, ds); !strings.Contains(ud, "acme-ddc-123456789012-us-east-1") {
					t.Errorf("rebuilt user data does not carry the prefixed bucket:\n%s", ud)
				}
			}
		})
	}

	sg, err := rebuildSGDesiredState(acmePrefix, &state.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup}, &state.ModuleState{Name: "ddc"}, config.Defaults())
	if err != nil {
		t.Fatalf("rebuild security group: %v", err)
	}
	if !strings.Contains(string(sg), `"GroupName":"acme-ddc-sg"`) {
		t.Errorf("rebuilt security group is not acme-ddc-sg: %s", sg)
	}
}

func TestRebuildDDCEdgeFollowsPrefix(t *testing.T) {
	cfg := config.Defaults()
	st := state.NewState("123456789012", homeTestRegion)
	mod := &state.ModuleState{
		Name: "ddc",
		Resources: []state.ModuleResource{
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-edge", Properties: map[string]string{
				"region": edgeTestRegion, "role": ddc.RoleEdge, "vpcId": "vpc-edge",
			}},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-edge", Properties: map[string]string{
				"region": edgeTestRegion, "role": ddc.RoleEdge, "imageId": "ami-edge",
				"subnetId": "subnet-edge", "rootDevice": "/dev/sda1",
			}},
			{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "acme-ddc-profile"},
		},
	}

	ds, err := rebuildEdgeInstanceDesiredState(acmePrefix, &mod.Resources[1], mod, st, cfg)
	if err != nil {
		t.Fatalf("rebuild edge instance: %v", err)
	}
	if !strings.Contains(string(ds), `"Value":"acme-ddc-edge-`+edgeTestRegion+`"`) {
		t.Errorf("rebuilt edge Name tag is not acme-ddc-edge-%s: %s", edgeTestRegion, ds)
	}

	sg, err := rebuildDDCSG(acmePrefix, &mod.Resources[0], cfg)
	if err != nil {
		t.Fatalf("rebuild edge security group: %v", err)
	}
	if !strings.Contains(string(sg), `"GroupName":"acme-ddc-sg-`+edgeTestRegion+`"`) {
		t.Errorf("rebuilt edge security group is not acme-ddc-sg-%s: %s", edgeTestRegion, sg)
	}
}
