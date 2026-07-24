package destroy

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
)

func TestDestroyWithoutAllPrintsUsageHint(t *testing.T) {
	var out bytes.Buffer
	cmd := command{out: &out}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "To destroy infrastructure, use --all:")
}

func TestDestroyAllDelegatesToOrchestrator(t *testing.T) {
	called := false
	c := command{
		all:       true,
		assumeYes: true,
		out:       &bytes.Buffer{},
		confirm:   func(string, string) bool { return true },
		runAll:    func(context.Context) error { called = true; return nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !called {
		t.Fatal("--all should delegate to the orchestrator seam")
	}
}
