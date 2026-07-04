package version

import (
	"bytes"
	"strings"
	"testing"

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
	orig := fabricav.Commit
	t.Cleanup(func() { fabricav.Commit = orig })
	fabricav.Commit = "abc1234"

	out := runVersion(t)
	for _, want := range []string{"Fabrica", "Commit: abc1234", "Go:", "OS/Arch:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("version output missing %q:\n%s", want, out)
		}
	}
}

func TestVersionWithoutCommit(t *testing.T) {
	orig := fabricav.Commit
	t.Cleanup(func() { fabricav.Commit = orig })
	fabricav.Commit = ""

	out := runVersion(t)
	if strings.Contains(out, "Commit:") {
		t.Fatalf("empty commit should omit the Commit line:\n%s", out)
	}
	if !strings.Contains(out, "Fabrica") || !strings.Contains(out, "OS/Arch:") {
		t.Fatalf("version output missing expected lines:\n%s", out)
	}
}
