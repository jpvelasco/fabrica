package create

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

func TestCreatePreflightBlocksIGWOnlySubnet(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.NetworkProvider{BPA: cloud.BPAStatus{Mode: cloud.BPAModeBlockIngress}, Subnet: testutil.IGWOnlySubnet("subnet-pub")}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.dryRun = true
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "horde cannot be placed") {
		t.Fatalf("run() error = %v, want the IGW-only preflight block", err)
	}
}
