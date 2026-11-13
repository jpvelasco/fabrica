package root_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/root"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
)

func TestVersionFlagMatchesSubcommand(t *testing.T) {
	originalVersion, originalCommit := fabricav.Version, fabricav.Commit
	t.Cleanup(func() {
		fabricav.Version, fabricav.Commit = originalVersion, originalCommit
	})

	for _, commit := range []string{"unknown", "abc1234"} {
		t.Run(commit, func(t *testing.T) {
			fabricav.Version, fabricav.Commit = "dev", commit
			configPath := filepath.Join(t.TempDir(), "missing.yaml")
			var outputs [2]string
			for i, arg := range []string{"version", "--version"} {
				var output bytes.Buffer
				cmd := root.New(&output)
				cmd.SetArgs([]string{"--config", configPath, arg})
				if err := cmd.Execute(); err != nil {
					t.Fatalf("%s: %v", arg, err)
				}
				outputs[i] = output.String()
			}

			firstLine, _, _ := strings.Cut(outputs[0], "\n")
			if outputs[1] != firstLine+"\n" {
				t.Fatalf("--version = %q; version first line = %q", outputs[1], firstLine)
			}
			if want := fmt.Sprintf("Fabrica %s\n", fabricav.String()); outputs[1] != want {
				t.Fatalf("--version = %q; want %q", outputs[1], want)
			}
		})
	}
}

func TestVersionFlagIgnoresRuntimeConfiguration(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(configPath, []byte("cloud: [invalid"), 0600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cmd := root.New(&output)
	cmd.SetArgs([]string{"--config", configPath, "--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--version should bypass invalid configuration: %v", err)
	}
	if want := fmt.Sprintf("Fabrica %s\n", fabricav.String()); output.String() != want {
		t.Fatalf("--version = %q; want %q", output.String(), want)
	}
}
