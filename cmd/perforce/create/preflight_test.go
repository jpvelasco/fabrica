package create

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

// bpaIngress is the account state that blocks IGW-only placement.
var bpaIngress = cloud.BPAStatus{Mode: cloud.BPAModeBlockIngress}

func TestCreatePreflightBlocksIGWOnlySubnet(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.NetworkProvider{BPA: bpaIngress, Subnet: testutil.IGWOnlySubnet("subnet-pub")}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "perforce cannot be placed: subnet subnet-pub reaches") {
		t.Fatalf("run() error = %v, want the IGW-only preflight block", err)
	}
}

func TestCreateDryRunWarnsOnEndpointsOnlySubnet(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.NetworkProvider{BPA: bpaIngress, Subnet: testutil.SSMOnlySubnet("subnet-ssm")}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.dryRun = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(out.String(), "  Warning:          subnet subnet-ssm reaches AWS through VPC endpoints only") {
		t.Fatalf("dry-run output missing the endpoints warning:\n%s", out.String())
	}
}
