package destroy

import (
	"bytes"
	"context"
	"testing"
)

func TestDestroyWithoutAllPrintsUsageHint(t *testing.T) {
	var out bytes.Buffer
	cmd := command{out: &out}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "To destroy infrastructure, use --all:")
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

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}
