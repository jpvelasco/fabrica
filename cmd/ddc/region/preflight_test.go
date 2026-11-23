package region

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

func TestRunPreflightBlocksIGWOnlySubnet(t *testing.T) {
	var buf bytes.Buffer
	c := testCommand(&buf, provisionedState(), nil)
	c.dryRun = true
	provider := &testutil.NetworkProvider{BPA: cloud.BPAStatus{Mode: cloud.BPAModeBlockIngress}, Subnet: testutil.IGWOnlySubnet("subnet-edge")}
	c.withRegion = func(context.Context, string) (cloud.RegionView, error) {
		return cloud.RegionView{Network: provider}, nil
	}
	if err := c.run(context.Background(), "eu-west-1"); err == nil || !strings.Contains(err.Error(), "ddc region add cannot be placed") {
		t.Fatalf("run() error = %v, want the IGW-only preflight block", err)
	}
}
