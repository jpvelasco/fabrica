package testutil

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// RunAMIRuntimeTests verifies offline AMI generation without a provider, config
// failures before file writes, and help without runtime resolution.
func RunAMIRuntimeTests(t *testing.T, newCmd func(globals.RuntimeSource, io.Writer) *cobra.Command) {
	t.Helper()
	sourceErr := errors.New("configuration source failed")
	for _, tc := range []struct {
		name, owner, want string
		sourceErr         error
		help              bool
	}{
		{name: "config without provider"},
		{name: "source failure", sourceErr: sourceErr, want: sourceErr.Error()},
		{name: "invalid identity", owner: "bad owner", want: "cloud.aws.identity.managedBy"},
		{name: "activation guarded", owner: "studio-tools", want: "custom ownership is not enabled"},
		{name: "help without runtime", sourceErr: sourceErr, help: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "output")
			calls := 0
			source := func() (globals.Runtime, error) {
				calls++
				cfg := &config.Config{}
				cfg.Cloud.AWS.Identity.ManagedBy = tc.owner
				cfg.Cloud.AWS.Tags = map[string]string{"ManagedBy": "foreign", "Project": "studio"}
				return globals.Runtime{Config: cfg}, tc.sourceErr
			}
			cmd := newCmd(source, io.Discard)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			cmd.SetOut(io.Discard)
			cmd.SetArgs([]string{"build", "--output-dir", dir})
			if tc.help {
				cmd.SetArgs([]string{"build", "--help"})
			}
			err := cmd.ExecuteContext(t.Context())
			switch {
			case tc.help:
				if err != nil || calls != 0 {
					t.Fatalf("help loaded runtime %d times or failed: %v", calls, err)
				}
			case tc.want != "":
				if err == nil || !strings.Contains(err.Error(), tc.want) || calls != 1 {
					t.Fatalf("runtime calls = %d, error = %v; want one call and %q", calls, err, tc.want)
				}
				if tc.sourceErr != nil && !errors.Is(err, sourceErr) {
					t.Fatalf("source error lost: %v", err)
				}
			default:
				if err != nil || calls != 1 {
					t.Fatalf("generation loaded runtime %d times or failed: %v", calls, err)
				}
			}
			if tc.help || tc.want != "" {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("help or failure created output directory: %v", err)
				}
				return
			}
			raw, err := fs.ReadFile(os.DirFS(dir), "image-builder-recipe.json")
			if err != nil {
				t.Fatal(err)
			}
			var recipe struct {
				Tags map[string]string `json:"tags"`
			}
			if err := json.Unmarshal(raw, &recipe); err != nil {
				t.Fatal(err)
			}
			if recipe.Tags["ManagedBy"] != "fabrica" {
				t.Fatalf("ordinary tag selected ownership: %v", recipe.Tags)
			}
		})
	}
}
