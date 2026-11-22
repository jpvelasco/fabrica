package driftcmd

import (
	"context"
	"testing"
)

func TestRun_RefusesGatedNamePrefix(t *testing.T) {
	c := newTestCommand(t)
	c.runtime.Config.Cloud.AWS.Identity.NamePrefix = "acme"
	if err := c.run(context.Background(), false); err == nil {
		t.Fatal("gated name prefix ran drift")
	}
}
