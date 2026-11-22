package driftcmd

import (
	"context"
	"testing"
)

func TestRun_RefusesInvalidNamePrefix(t *testing.T) {
	c := newTestCommand(t)
	c.runtime.Config.Cloud.AWS.Identity.NamePrefix = "Bad_Prefix"
	if err := c.run(context.Background(), false); err == nil {
		t.Fatal("gated name prefix ran drift")
	}
}
