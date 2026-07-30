// Package ec2plan provides a shared base struct and constructor for EC2-based
// module plans. All single-instance modules (horde, lore, perforce, workstation)
// embed [Base] in their CreatePlan and call [New] to populate the common fields
// (account, region, instance type, volume, CIDR, VPC topology, naming).
//
// This eliminates the duplicated default-resolution, VPC-resolution, and struct
// population code that was copy-pasted across each module's plan.go.
package ec2plan

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/topology"
)

// Base holds the common EC2 fields shared by all single-instance modules.
// Embed this in your CreatePlan — fields are promoted, so plan.InstanceType
// still works without qualification.
type Base struct {
	Account      string
	Region       string
	InstanceType string
	VolumeSize   int
	AllowedCIDR  string
	VPCID        string
	SubnetID     string
	DefaultVPC   bool
	SGName       string
	InstanceName string
}

// Params carries the inputs for [New]. The module resolves its own defaults
// (instance type, volume size, CIDR) before passing them here. Validation
// and module-specific defaults remain in the module's own NewCreatePlan.
type Params struct {
	Account      string
	Region       string
	ModuleName   string // used for SGName / InstanceName convention
	InstanceType string // already resolved with module defaults
	VolumeSize   int    // already resolved with module defaults
	AllowedCIDR  string // already resolved with module defaults
	VPCId        string // from config (may be empty, triggers EC2 lookup)
	SubnetId     string // from config (may be empty, triggers EC2 lookup)
	Resolver     cloud.VPCResolver
}

// New builds the common EC2 fields, resolving VPC topology and applying
// the module naming convention. Returns a Base ready to embed in a CreatePlan.
func New(ctx context.Context, p Params) (*Base, error) {
	vpcID, subnetID, defaultVPC, err := topology.ResolveVPC(ctx, p.VPCId, p.SubnetId, p.Resolver)
	if err != nil {
		return nil, err
	}
	return &Base{
		Account:      p.Account,
		Region:       p.Region,
		InstanceType: p.InstanceType,
		VolumeSize:   p.VolumeSize,
		AllowedCIDR:  p.AllowedCIDR,
		VPCID:        vpcID,
		SubnetID:     subnetID,
		DefaultVPC:   defaultVPC,
		SGName:       fmt.Sprintf("fabrica-%s-sg", p.ModuleName),
		InstanceName: fmt.Sprintf("fabrica-%s", p.ModuleName),
	}, nil
}

// StringOr returns value if non-empty, otherwise fallback.
func StringOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// IntOrPositive returns value if positive, otherwise fallback.
func IntOrPositive(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}
