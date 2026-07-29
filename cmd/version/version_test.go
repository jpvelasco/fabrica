package version

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
)

// runVersion executes the command's RunE with a captured buffer.
func runVersion(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	Cmd.SetOut(&buf)
	Cmd.SetErr(&buf)
	if err := Cmd.RunE(Cmd, nil); err != nil {
		t.Fatalf("version RunE: %v", err)
	}
	return buf.String()
}

func TestVersionWithCommit(t *testing.T) {
	origV, origC := fabricav.Version, fabricav.Commit
	t.Cleanup(func() {
		fabricav.Version, fabricav.Commit = origV, origC
	})
	fabricav.Version = "dev"
	fabricav.Commit = "abc1234"

	out := runVersion(t)
	for _, want := range []string{"Fabrica", "dev (abc1234)", "Go:", "OS/Arch:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("version output missing %q:\n%s", want, out)
		}
	}
	testutil.AssertNotContains(t, out, "Commit:")
}

func TestVersionWithoutCommit(t *testing.T) {
	origV, origC := fabricav.Version, fabricav.Commit
	t.Cleanup(func() {
		fabricav.Version, fabricav.Commit = origV, origC
	})
	fabricav.Version = "dev"
	fabricav.Commit = "unknown"

	out := runVersion(t)
	testutil.AssertNotContains(t, out, "Commit:")
	testutil.AssertNotContains(t, out, "(unknown)")
	if !strings.Contains(out, "Fabrica") || !strings.Contains(out, "OS/Arch:") {
		t.Fatalf("version output missing expected lines:\n%s", out)
	}
}
