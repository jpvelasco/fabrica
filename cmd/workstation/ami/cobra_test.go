package ami_test

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/workstation/ami"
	"github.com/spf13/cobra"
)

func TestBuildHelp(t *testing.T) {
	var out bytes.Buffer
	root := &cobra.Command{Use: "fabrica"}
	root.PersistentFlags().Bool("dry-run", false, "")
	root.PersistentFlags().Bool("yes", false, "")
	root.PersistentFlags().Bool("json", false, "")
	root.AddCommand(ami.New(&out))
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"ami", "build", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("NICE DCV")) {
		t.Errorf("help missing DCV: %s", out.String())
	}
}
