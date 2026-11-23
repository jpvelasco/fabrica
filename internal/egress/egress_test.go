package egress

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

const rtb = "rtb-1"

// ssm returns the SSM control-plane set; the S3 gateway endpoint is attached to rtb.
func ssm(s3RouteTable string, state string) []cloud.VPCEndpoint {
	return []cloud.VPCEndpoint{
		{ServiceName: "com.amazonaws.us-west-2.ssm", Type: "Interface", State: state},
		{ServiceName: "com.amazonaws.us-west-2.ssmmessages", Type: "Interface", State: state},
		{ServiceName: "com.amazonaws.us-west-2.ec2messages", Type: "Interface", State: state},
		{ServiceName: "com.amazonaws.us-west-2.s3", Type: "Gateway", State: state, RouteTableIDs: []string{s3RouteTable}},
	}
}

func subnet(route cloud.DefaultRouteKind, eps ...cloud.VPCEndpoint) cloud.SubnetNetwork {
	return cloud.SubnetNetwork{SubnetID: "subnet-a", VPCID: "vpc-1", RouteTableID: rtb, DefaultRoute: route, Endpoints: eps}
}

func bpa(mode string, excl ...cloud.BPAExclusion) cloud.BPAStatus {
	return cloud.BPAStatus{Mode: mode, Exclusions: excl}
}

func TestClassify(t *testing.T) {
	ingress, bidi := cloud.BPAModeBlockIngress, cloud.BPAModeBlockBidirectional
	tests := []struct {
		name     string
		bpa      cloud.BPAStatus
		net      cloud.SubnetNetwork
		internet bool
		wantLvl  Level
		wantPath Path
		wantText string
	}{
		{"off ignores igw", bpa(cloud.BPAModeOff), subnet(cloud.RouteIGW), false, LevelOK, PathIGWOnly, ""},
		{"empty mode is off", bpa(""), subnet(cloud.RouteIGW), false, LevelOK, PathIGWOnly, ""},
		{"igw fails under ingress block", bpa(ingress), subnet(cloud.RouteIGW), false, LevelFail, PathIGWOnly, "internet gateway"},
		{"igw lifted by subnet allow-bidirectional", bpa(ingress, cloud.BPAExclusion{ResourceID: "subnet-a", Mode: cloud.BPAExclusionAllowBidirectional}), subnet(cloud.RouteIGW), false, LevelOK, PathIGWOnly, ""},
		{"igw lifted by vpc allow-bidirectional", bpa(ingress, cloud.BPAExclusion{ResourceID: "vpc-1", Mode: cloud.BPAExclusionAllowBidirectional}), subnet(cloud.RouteIGW), false, LevelOK, PathIGWOnly, ""},
		{"allow-egress ignored under ingress block", bpa(ingress, cloud.BPAExclusion{ResourceID: "subnet-a", Mode: cloud.BPAExclusionAllowEgress}), subnet(cloud.RouteIGW), false, LevelFail, PathIGWOnly, "internet gateway"},
		{"allow-egress lifts under bidirectional block", bpa(bidi, cloud.BPAExclusion{ResourceID: "vpc-1", Mode: cloud.BPAExclusionAllowEgress}), subnet(cloud.RouteIGW), false, LevelOK, PathIGWOnly, ""},
		{"exclusion for another resource does not lift", bpa(ingress, cloud.BPAExclusion{ResourceID: "subnet-other", Mode: cloud.BPAExclusionAllowBidirectional}), subnet(cloud.RouteIGW), false, LevelFail, PathIGWOnly, "internet gateway"},
		{"igw beside full endpoints stays manageable", bpa(ingress), subnet(cloud.RouteIGW, ssm(rtb, "Available")...), false, LevelOK, PathEndpoints, ""},
		{"igw beside endpoints warns when internet needed", bpa(ingress), subnet(cloud.RouteIGW, ssm(rtb, "Available")...), true, LevelWarn, PathEndpoints, "downloads packages"},
		{"nat ok", bpa(ingress), subnet(cloud.RouteNAT), true, LevelOK, PathNAT, ""},
		{"private transit gateway ok", bpa(bidi), subnet(cloud.RoutePrivate), true, LevelOK, PathPrivate, ""},
		{"endpoints ok for AMI-first module", bpa(ingress), subnet(cloud.RouteNone, ssm(rtb, "Available")...), false, LevelOK, PathEndpoints, ""},
		{"endpoints warn when internet needed at boot", bpa(ingress), subnet(cloud.RouteNone, ssm(rtb, "Available")...), true, LevelWarn, PathEndpoints, "downloads packages"},
		{"isolated without endpoints fails", bpa(ingress), subnet(cloud.RouteNone), false, LevelFail, PathIsolated, "no full set of SSM endpoints"},
		{"gateway endpoint off the subnet route table is isolated", bpa(ingress), subnet(cloud.RouteNone, ssm("rtb-other", "Available")...), false, LevelFail, PathIsolated, "no full set"},
		{"unavailable endpoints are isolated", bpa(ingress), subnet(cloud.RouteNone, ssm(rtb, "Pending")...), false, LevelFail, PathIsolated, "no full set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.bpa, tt.net, tt.internet)
			if got.Level != tt.wantLvl || got.Path != tt.wantPath {
				t.Fatalf("Classify = %s/%s, want %s/%s (%q)", got.Level, got.Path, tt.wantLvl, tt.wantPath, got.Message)
			}
			if !strings.Contains(got.Message, tt.wantText) {
				t.Fatalf("message %q does not contain %q", got.Message, tt.wantText)
			}
			// Every fail message must name the operator's fix.
			if got.Level == LevelFail && !strings.Contains(got.Message, "docs/ssm-private.md") {
				t.Fatalf("fail message %q does not name the fix", got.Message)
			}
		})
	}
}
