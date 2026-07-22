package root_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/root"
	"github.com/spf13/cobra"
)

// builtins are cobra-generated commands that are not part of Fabrica's
// documented surface.
var builtins = map[string]bool{"help": true, "completion": true}

// leafCommandPaths walks the command tree and returns the space-joined path of
// every runnable leaf command (a command with no subcommands that has a RunE),
// relative to the root (the root's own name is dropped). Built-in subtrees are
// skipped. destroy --all is NOT a subcommand — it is a flag on the runnable
// leaf `destroy`, so it is covered by asserting `destroy` is documented; the
// `--all` prose itself is verified manually, not by this guard.
func leafCommandPaths(c *cobra.Command, prefix []string) []string {
	if builtins[c.Name()] {
		return nil
	}
	// The root command itself carries no path segment.
	var here []string
	if len(prefix) == 0 && c.Parent() == nil {
		here = nil
	} else {
		here = append(append([]string{}, prefix...), c.Name())
	}

	children := c.Commands()
	var out []string
	hasRunnableChild := false
	for _, sub := range children {
		if builtins[sub.Name()] {
			continue
		}
		hasRunnableChild = true
		out = append(out, leafCommandPaths(sub, here)...)
	}
	// A leaf = no (non-builtin) children AND runnable.
	if !hasRunnableChild && c.Runnable() && len(here) > 0 {
		out = append(out, strings.Join(here, " "))
	}
	return out
}

func TestEveryCommandIsDocumented(t *testing.T) {
	rootCmd := root.New(io.Discard)

	// README lives at the repo root, two dirs up from cmd/root/.
	readmePath := filepath.Clean(filepath.Join("..", "..", "README.md"))
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("reading README (%s): %v", readmePath, err)
	}
	readme := string(data)

	paths := leafCommandPaths(rootCmd, nil)
	if len(paths) == 0 {
		t.Fatal("no leaf commands found — the tree walk is broken")
	}

	var missing []string
	for _, p := range paths {
		if !strings.Contains(readme, p) {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		t.Errorf("these commands are not documented in README.md (add them, or the doc-drift guard fails):\n  %s",
			strings.Join(missing, "\n  "))
	}
}
