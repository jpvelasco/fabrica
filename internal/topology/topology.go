// Package topology provides provider-agnostic coordinator/edge graph types.
// Used by Distributed DDC: the home region hosts a co-located coordinator +
// edge node, and additional regions add edge-only nodes via WithRemoteEdge.
package topology

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

// Role is the logical role of a node in a distributed topology.
type Role string

const (
	// RoleCoordinator is the control-plane / primary service node.
	RoleCoordinator Role = "coordinator"
	// RoleEdge is a regional cache/serving node.
	RoleEdge Role = "edge"
)

// NodeSpec describes one logical node without cloud provider type names.
type NodeSpec struct {
	Role         Role
	Region       string
	InstanceType string
	AmiID        string
	VolumeSize   int
}

// Topology is a home-region coordinator plus optional edge nodes.
// V1 DDC uses NewHomeCoLocated to record one co-located home host; additional
// edge regions are appended with WithRemoteEdge.
type Topology struct {
	HomeRegion  string
	Coordinator NodeSpec
	// Edges includes the co-located home edge (Region == HomeRegion) and any
	// remote edges added via WithRemoteEdge (Region != HomeRegion).
	Edges []NodeSpec
}

// NewHomeCoLocated builds a single-host topology: coordinator and home edge
// roles share the same region and instance shape (co-located on one EC2).
func NewHomeCoLocated(region string, node NodeSpec) Topology {
	coord := node
	coord.Role = RoleCoordinator
	coord.Region = region

	edge := node
	edge.Role = RoleEdge
	edge.Region = region

	return Topology{
		HomeRegion:  region,
		Coordinator: coord,
		Edges:       []NodeSpec{edge},
	}
}

// WithRemoteEdge appends an edge node in another region, stamping the role so
// callers can pass a plain NodeSpec. Returns a copy; does not mutate t.
func (t Topology) WithRemoteEdge(edge NodeSpec) Topology {
	edge.Role = RoleEdge
	if edge.Region != "" {
		t.Edges = append(t.Edges, edge)
	}
	return t
}

// Validate checks structural invariants. The coordinator must stay in the home
// region; edge regions may differ (multi-region graphs are valid).
func (t Topology) Validate() error {
	if t.HomeRegion == "" {
		return fmt.Errorf("topology: HomeRegion is required")
	}
	if t.Coordinator.Role != "" && t.Coordinator.Role != RoleCoordinator {
		return fmt.Errorf("topology: Coordinator.Role must be %q, got %q", RoleCoordinator, t.Coordinator.Role)
	}
	if t.Coordinator.Region != "" && t.Coordinator.Region != t.HomeRegion {
		return fmt.Errorf("topology: Coordinator.Region %q must match HomeRegion %q", t.Coordinator.Region, t.HomeRegion)
	}
	for i, e := range t.Edges {
		if e.Region == "" {
			return fmt.Errorf("topology: edge[%d] region is required", i)
		}
		if e.Region == t.HomeRegion && i > 0 {
			return fmt.Errorf("topology: edge[%d] duplicates the home region (only the co-located home edge may use %q)", i, t.HomeRegion)
		}
		if e.Role != "" && e.Role != RoleEdge {
			return fmt.Errorf("topology: edge[%d] Role must be %q, got %q", i, RoleEdge, e.Role)
		}
	}
	return nil
}

// Regions returns the unique regions referenced by the topology.
func (t Topology) Regions() []string {
	seen := map[string]bool{}
	var out []string
	add := func(r string) {
		if r == "" || seen[r] {
			return
		}
		seen[r] = true
		out = append(out, r)
	}
	add(t.HomeRegion)
	add(t.Coordinator.Region)
	for _, e := range t.Edges {
		add(e.Region)
	}
	return out
}

// ResolveVPC returns the effective VPC ID, subnet ID, and whether the
// default VPC was resolved via the resolver. If both vpcID and subnetID are
// set, they win as-is. If both are empty and resolver is non-nil, the account
// default VPC is resolved. A partially specified pair is rejected with an
// actionable error rather than silently substituting default-VPC values for
// an explicitly configured one.
func ResolveVPC(ctx context.Context, vpcID, subnetID string, resolver cloud.VPCResolver) (string, string, bool, error) {
	defaultVPC := false
	switch {
	case vpcID != "" && subnetID != "":
		// Explicit configuration wins; nothing to resolve.
	case vpcID == "" && subnetID == "":
		if resolver != nil {
			var err error
			vpcID, subnetID, err = resolver.ResolveDefaultVPC(ctx)
			if err != nil {
				return "", "", false, fmt.Errorf("resolving default VPC: %w", err)
			}
			defaultVPC = true
		}
	default:
		return "", "", false, fmt.Errorf(
			"vpcId and subnetId must be set together or both omitted (got vpcId=%q, subnetId=%q) — omit both to place resources in the account default VPC",
			vpcID, subnetID,
		)
	}
	return vpcID, subnetID, defaultVPC, nil
}
