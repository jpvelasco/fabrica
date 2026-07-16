// Package topology provides provider-agnostic coordinator/edge graph types.
// Used by Distributed DDC (and future multi-region modules). V1 materializes
// only a single home-region host with co-located coordinator + edge roles.
package topology

import "fmt"

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
// V1 DDC uses NewHomeCoLocated: one physical host, both roles recorded.
type Topology struct {
	HomeRegion  string
	Coordinator NodeSpec
	// Edges may include a single co-located home edge (same region as HomeRegion).
	// Remote edges (Region != HomeRegion) are rejected by Validate for V1 callers.
	Edges []NodeSpec
}

// NewHomeCoLocated builds a V1 single-host topology: coordinator and home edge
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

// Validate checks structural invariants. Rejects edges outside HomeRegion so
// V1 code cannot accidentally encode multi-region graphs.
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
		if e.Region != "" && e.Region != t.HomeRegion {
			return fmt.Errorf("topology: edge[%d] region %q is outside HomeRegion %q (multi-region edges are not valid in V1 graphs)", i, e.Region, t.HomeRegion)
		}
		if e.Role != "" && e.Role != RoleEdge {
			return fmt.Errorf("topology: edge[%d] Role must be %q, got %q", i, RoleEdge, e.Role)
		}
	}
	return nil
}

// Regions returns the unique regions referenced (V1: only HomeRegion when valid).
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
