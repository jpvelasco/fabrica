// Package egress classifies whether a subnet can reach AWS control-plane
// services while VPC Block Public Access (BPA) is on. It is pure: callers pass
// the inspected cloud.BPAStatus and cloud.SubnetNetwork.
package egress

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

// Level is the outcome of a classification.
type Level string

const (
	LevelOK   Level = "ok"
	LevelWarn Level = "warn"
	LevelFail Level = "fail"
)

// Path is the egress path a subnet offers.
type Path string

const (
	PathNAT       Path = "nat"
	PathEndpoints Path = "endpoints"
	PathIGWOnly   Path = "igw-only"
	PathPrivate   Path = "private"
	PathIsolated  Path = "isolated"
)

// Verdict is one subnet's classification. Message is empty when Level is LevelOK.
type Verdict struct {
	Level   Level
	Path    Path
	Message string
}

const fixHint = "use a private subnet with a NAT gateway, or add the ssm, ssmmessages and ec2messages interface endpoints plus the S3 gateway endpoint (docs/ssm-private.md)"

// requiredEndpoints is the SSM control-plane set: service-name suffix and
// endpoint type. Gateway endpoints must also be attached to the subnet's route table.
var requiredEndpoints = []struct{ suffix, typ string }{
	{".ssm", "Interface"},
	{".ssmmessages", "Interface"},
	{".ec2messages", "Interface"},
	{".s3", "Gateway"},
}

// Classify grades a subnet against the BPA configuration. With BPA off every
// subnet is OK. With BPA on, an IGW-only subnet fails unless an exclusion lifts
// the block, an isolated subnet always fails, and an endpoint-only subnet warns
// when internetAtBoot is set (the module downloads packages during boot).
func Classify(bpa cloud.BPAStatus, net cloud.SubnetNetwork, internetAtBoot bool) Verdict {
	p := pathOf(net)
	if !bpa.On() {
		return Verdict{Level: LevelOK, Path: p}
	}
	switch p {
	case PathIGWOnly:
		if lifted(bpa, net) {
			return Verdict{Level: LevelOK, Path: p}
		}
		return Verdict{Level: LevelFail, Path: p, Message: fmt.Sprintf("subnet %s reaches the internet only through an internet gateway, which VPC Block Public Access (%s) blocks. Fix: %s.", net.SubnetID, bpa.Mode, fixHint)}
	case PathIsolated:
		return Verdict{Level: LevelFail, Path: p, Message: fmt.Sprintf("subnet %s has no default route and no full set of SSM endpoints (ssm, ssmmessages, ec2messages and S3). Fix: %s.", net.SubnetID, fixHint)}
	case PathEndpoints:
		if internetAtBoot {
			return Verdict{Level: LevelWarn, Path: p, Message: fmt.Sprintf("subnet %s reaches AWS through VPC endpoints only, but this module downloads packages from the internet at boot. Fix: use a private subnet with a NAT gateway (docs/ssm-private.md).", net.SubnetID)}
		}
	}
	return Verdict{Level: LevelOK, Path: p}
}

// pathOf picks the egress path. NAT and private routes win. Otherwise a full
// SSM endpoint set keeps the subnet manageable (BPA does not block VPC
// endpoints, even beside an IGW route); without one it is IGW-only or isolated.
func pathOf(net cloud.SubnetNetwork) Path {
	switch net.DefaultRoute {
	case cloud.RouteNAT:
		return PathNAT
	case cloud.RoutePrivate:
		return PathPrivate
	}
	if hasSSMEndpoints(net) {
		return PathEndpoints
	}
	if net.DefaultRoute == cloud.RouteIGW {
		return PathIGWOnly
	}
	return PathIsolated
}

// hasSSMEndpoints reports whether every required SSM control-plane endpoint is present.
func hasSSMEndpoints(net cloud.SubnetNetwork) bool {
	for _, req := range requiredEndpoints {
		if !hasEndpoint(net, req.suffix, req.typ) {
			return false
		}
	}
	return true
}

// hasEndpoint reports whether an available endpoint of typ matches the service
// suffix. Gateway endpoints count only when attached to the subnet's route table.
func hasEndpoint(net cloud.SubnetNetwork, suffix, typ string) bool {
	for _, e := range net.Endpoints {
		attached := typ != "Gateway" || slices.Contains(e.RouteTableIDs, net.RouteTableID)
		if strings.EqualFold(e.State, "available") && e.Type == typ && strings.HasSuffix(e.ServiceName, suffix) && attached {
			return true
		}
	}
	return false
}

// lifted reports whether an exclusion on the subnet or its VPC lifts the block
// for an IGW-only subnet. allow-bidirectional always lifts it; allow-egress
// lifts it only under block-bidirectional, the one mode where it applies.
func lifted(bpa cloud.BPAStatus, net cloud.SubnetNetwork) bool {
	for _, ex := range bpa.Exclusions {
		if ex.ResourceID != net.SubnetID && ex.ResourceID != net.VPCID {
			continue
		}
		if ex.Mode == cloud.BPAExclusionAllowBidirectional ||
			(ex.Mode == cloud.BPAExclusionAllowEgress && bpa.Mode == cloud.BPAModeBlockBidirectional) {
			return true
		}
	}
	return false
}
