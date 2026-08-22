package costsource

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/jpvelasco/fabrica/internal/deploy"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/lore"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/workstation"
)

// TestModuleCostResourcesEstimable is a cross-module guard: every default,
// scylla, and workstation-template shape must have a registered estimator. A
// missing entry surfaces as "(no estimate)" in cost report and a wrong
// pre-approval estimate at create time.
func TestModuleCostResourcesEstimable(t *testing.T) {
	cases := []struct {
		module    string
		resources []cost.Resource
	}{
		{"perforce", perforce.CostResources(config.PerforceConfig{})},
		{"horde", horde.CostResources(config.HordeConfig{})},
		{"lore", lore.CostResources(config.LoreConfig{})},
		{"ddc", ddc.CostResources(config.DDCConfig{})},
		{"ddc-scylla", ddc.CostResources(config.DDCConfig{Backend: ddc.BackendScylla})},
		{"workstation", workstation.CostResources(config.WorkstationConfig{})},
		{"workstation-artist", workstation.CostResourcesFor(workstation.ArtistInstanceType, workstation.ArtistVolumeSize)},
		{"workstation-programmer", workstation.CostResourcesFor(workstation.ProgrammerInstanceType, workstation.ProgrammerVolumeSize)},
		{"ci", ci.CostResources(config.CIConfig{})},
	}
	for _, tc := range cases {
		if len(tc.resources) == 0 {
			t.Errorf("%s: CostResources returned no resources", tc.module)
			continue
		}
		for _, r := range tc.resources {
			if _, err := cost.Global.Estimate(r.TypeName, r); err != nil {
				t.Errorf("%s: %s %q not estimable: %v", tc.module, r.TypeName, r.Name, err)
			}
		}
	}
}

// TestDeployFleetCostShapeEstimable verifies the fleet estimator accepts the
// default deploy shape (deploy reports zero cost until a fleet exists).
func TestDeployFleetCostShapeEstimable(t *testing.T) {
	for _, r := range deploy.CostResources(config.DeployConfig{}) {
		if _, err := cost.Global.Estimate(r.TypeName, r); err != nil {
			t.Errorf("deploy: %s %q not estimable: %v", r.TypeName, r.Name, err)
		}
	}
}
