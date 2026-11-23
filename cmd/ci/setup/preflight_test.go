package setup

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

func TestSetupPreflightBlocksIGWOnlySubnet(t *testing.T) {
	var out bytes.Buffer
	c, _ := newCmd(&out, nil, true)
	c.dryRun = true
	c.runtime.Provider = &testutil.NetworkProvider{BPA: cloud.BPAStatus{Mode: cloud.BPAModeBlockIngress}, Subnet: testutil.IGWOnlySubnet("subnet-pub")}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "ci cannot be placed") {
		t.Fatalf("run() error = %v, want the IGW-only preflight block", err)
	}
}
