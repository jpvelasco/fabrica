package workstation

import (
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/ec2cost"
)

// resolveSizing applies template + config + default precedence for the two
// cost-relevant fields. tmpl is "", TemplateArtist, or TemplateProgrammer.
func resolveSizing(cfg config.WorkstationConfig, tmpl string) (instanceType string, volumeSize int) {
	instanceType = cfg.InstanceType
	volumeSize = cfg.VolumeSize
	switch tmpl {
	case TemplateArtist:
		instanceType, volumeSize = ArtistInstanceType, ArtistVolumeSize
	case TemplateProgrammer:
		instanceType, volumeSize = ProgrammerInstanceType, ProgrammerVolumeSize
	}
	if instanceType == "" {
		instanceType = DefaultInstanceType
	}
	if volumeSize <= 0 {
		volumeSize = DefaultVolumeSize
	}
	return instanceType, volumeSize
}

// CostResources returns the cost inputs for a workstation at the given config.
// The cost path uses no template (tmpl=""), so it reflects config + defaults;
// a template only applies at create time.
func CostResources(cfg config.WorkstationConfig) []cost.Resource {
	instanceType, volumeSize := resolveSizing(cfg, "")
	return ec2cost.InstanceAndVolume(instanceType, volumeSize)
}
