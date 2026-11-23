package provision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

var (
	errDenied     = fmt.Errorf("describing VPC Block Public Access options: %w: UnauthorizedOperation", cloud.ErrInspectionDenied)
	igwSubnet     = testutil.IGWOnlySubnet("subnet-pub")
	natSubnet     = cloud.SubnetNetwork{SubnetID: "subnet-priv", VPCID: "vpc-1", DefaultRoute: cloud.RouteNAT}
	ssmSubnet     = testutil.SSMOnlySubnet("subnet-ssm")
	blockIngress  = cloud.BPAStatus{Mode: cloud.BPAModeBlockIngress}
	defaultTarget = NetworkTarget{Module: "perforce", Placement: "perforce.vpcId and perforce.subnetId", SubnetID: "subnet-pub"}
)

// fakeInspector serves canned BPA and subnet results and counts subnet calls.
type fakeInspector struct {
	bpa       cloud.BPAStatus
	bpaErr    error
	subnet    cloud.SubnetNetwork
	subnetErr error
	subnets   int
}

func (f *fakeInspector) BlockPublicAccess(context.Context) (cloud.BPAStatus, error) {
	return f.bpa, f.bpaErr
}

func (f *fakeInspector) SubnetEgress(context.Context, string) (cloud.SubnetNetwork, error) {
	f.subnets++
	return f.subnet, f.subnetErr
}

func TestNetworkPreflight(t *testing.T) {
	tests := []struct {
		name     string
		insp     cloud.NetworkInspector
		target   NetworkTarget
		wantWarn string
		wantErr  string
	}{
		{name: "nil inspector warns", insp: nil, target: defaultTarget, wantWarn: "not checked"},
		{name: "bpa denied warns", insp: &fakeInspector{bpaErr: errDenied}, target: defaultTarget, wantWarn: "ec2:DescribeVpcBlockPublicAccessOptions"},
		{name: "bpa other error blocks", insp: &fakeInspector{bpaErr: errors.New("throttled")}, target: defaultTarget, wantErr: "checking VPC Block Public Access"},
		{name: "bpa off passes", insp: &fakeInspector{bpa: cloud.BPAStatus{Mode: cloud.BPAModeOff}, subnet: igwSubnet}, target: defaultTarget},
		{name: "default vpc refused", insp: &fakeInspector{bpa: blockIngress}, target: NetworkTarget{Module: "perforce", Placement: "perforce.vpcId and perforce.subnetId", DefaultVPC: true}, wantErr: "will not fall back to the default VPC. Set perforce.vpcId and perforce.subnetId"},
		{name: "subnet denied warns", insp: &fakeInspector{bpa: blockIngress, subnetErr: errDenied}, target: defaultTarget, wantWarn: "were not checked"},
		{name: "subnet other error blocks", insp: &fakeInspector{bpa: blockIngress, subnetErr: errors.New("boom")}, target: defaultTarget, wantErr: "checking egress for subnet "},
		{name: "igw subnet blocks", insp: &fakeInspector{bpa: blockIngress, subnet: igwSubnet}, target: defaultTarget, wantErr: "cannot be placed: subnet subnet-pub"},
		{name: "nat subnet passes", insp: &fakeInspector{bpa: blockIngress, subnet: natSubnet}, target: defaultTarget},
		{name: "endpoints warn for boot-time internet", insp: &fakeInspector{bpa: blockIngress, subnet: ssmSubnet}, target: NetworkTarget{Module: "perforce", SubnetID: "subnet-ssm", InternetAtBoot: true}, wantWarn: "downloads packages"},
		{name: "endpoints pass for AMI-first module", insp: &fakeInspector{bpa: blockIngress, subnet: ssmSubnet}, target: NetworkTarget{Module: "ci", SubnetID: "subnet-ssm"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings, err := NetworkPreflight(context.Background(), tt.insp, tt.target)
			if tt.wantErr != "" {
				assert.Contains(t, errString(err), tt.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("NetworkPreflight: %v", err)
			}
			if tt.wantWarn == "" {
				if len(warnings) != 0 {
					t.Fatalf("warnings = %v, want none", warnings)
				}
				return
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], tt.wantWarn) {
				t.Fatalf("warnings = %v, want one containing %q", warnings, tt.wantWarn)
			}
		})
	}
}

func TestNetworkPreflightSkipsSubnetWhenBPAOff(t *testing.T) {
	f := &fakeInspector{bpa: cloud.BPAStatus{Mode: cloud.BPAModeOff}, subnet: igwSubnet}
	if _, err := NetworkPreflight(context.Background(), f, defaultTarget); err != nil || f.subnets != 0 {
		t.Fatalf("err = %v, subnet inspections = %d; want none when BPA is off", err, f.subnets)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// inspectingProvider is a provider that also inspects network placement.
type inspectingProvider struct {
	testutil.TestProvider
	fakeInspector
}

func TestPreflightForBindsProviderInspector(t *testing.T) {
	p := &inspectingProvider{fakeInspector: fakeInspector{bpa: blockIngress, subnet: igwSubnet}}
	if _, err := PreflightFor(p, defaultTarget)(context.Background()); err == nil {
		t.Fatal("PreflightFor with an IGW-only subnet = nil error, want block")
	}
	if _, err := PreflightFor(&testutil.TestProvider{}, defaultTarget)(context.Background()); err != nil {
		t.Fatalf("provider without inspector: %v, want warning only", err)
	}
}

func TestWriteWarnings(t *testing.T) {
	var out bytes.Buffer
	WriteWarnings(&out, []string{"first", "second"})
	assert.Contains(t, out.String(), "Warning: first\nWarning: second\n")
}
