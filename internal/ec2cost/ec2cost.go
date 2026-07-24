// Package ec2cost provides shared cost-resource builders for EC2-based modules.
// All modules that provision an EC2 instance (horde, lore, workstation) use this
// instead of duplicating the EC2 instance + volume resource construction.
package ec2cost

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cost"
)

// InstanceAndVolume returns the standard EC2 instance + EBS volume cost resources
// for any EC2-based module. Callers resolve their module-specific defaults before
// calling this helper.
func InstanceAndVolume(instanceType string, volumeSize int) []cost.Resource {
	return []cost.Resource{
		{TypeName: "AWS::EC2::Instance", Name: instanceType},
		{TypeName: "AWS::EC2::Volume", Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
	}
}
