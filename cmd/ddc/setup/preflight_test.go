package setup

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestRunPreflightBlocksIGWOnlySubnet(t *testing.T) {
	var buf bytes.Buffer
	rt := testRuntime()
	rt.Provider = &testutil.NetworkProvider{BPA: cloud.BPAStatus{Mode: cloud.BPAModeBlockIngress}, Subnet: testutil.IGWOnlySubnet("subnet-1")}
	c := command{
		runtime: rt, dryRun: true, out: &buf, costs: fabricacost.Global,
		readState: func() (*fabricastate.State, error) { return &fabricastate.State{}, nil },
	}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "ddc cannot be placed") {
		t.Fatalf("run() error = %v, want the IGW-only preflight block", err)
	}
}
