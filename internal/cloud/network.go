package cloud

import (
	"context"
	"errors"
)

// ErrInspectionDenied is wrapped by NetworkInspector errors caused by a missing
// IAM permission; preflight degrades to a warning on it instead of blocking.
var ErrInspectionDenied = errors.New("network inspection denied by IAM")

// VPC Block Public Access modes and exclusion modes, as the provider reports them.
const (
	BPAModeOff                     = "off"
	BPAModeBlockBidirectional      = "block-bidirectional"
	BPAModeBlockIngress            = "block-ingress"
	BPAExclusionAllowBidirectional = "allow-bidirectional"
	BPAExclusionAllowEgress        = "allow-egress"
)

// DefaultRouteKind classifies a subnet's 0.0.0.0/0 route.
type DefaultRouteKind string

const (
	RouteNone    DefaultRouteKind = "none"
	RouteIGW     DefaultRouteKind = "igw"
	RouteNAT     DefaultRouteKind = "nat"
	RoutePrivate DefaultRouteKind = "private" // transit gateway, ENI/instance, peering, virtual gateway
)

// NetworkInspector reads the placement facts create preflight needs.
type NetworkInspector interface {
	BlockPublicAccess(ctx context.Context) (BPAStatus, error)
	SubnetEgress(ctx context.Context, subnetID string) (SubnetNetwork, error)
}

// BPAStatus is the region's VPC Block Public Access configuration.
type BPAStatus struct {
	Mode       string
	State      string
	Exclusions []BPAExclusion
}

type BPAExclusion struct {
	ResourceID string
	Mode       string
}

// On reports whether VPC Block Public Access restricts internet traffic.
func (s BPAStatus) On() bool {
	return s.Mode != "" && s.Mode != BPAModeOff
}

// SubnetNetwork is the egress picture for one subnet.
type SubnetNetwork struct {
	SubnetID     string
	VPCID        string
	RouteTableID string
	DefaultRoute DefaultRouteKind
	Endpoints    []VPCEndpoint
}

// VPCEndpoint is one endpoint in the subnet's VPC.
type VPCEndpoint struct {
	ServiceName   string
	Type          string
	State         string
	RouteTableIDs []string
}
