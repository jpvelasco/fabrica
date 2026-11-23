package hordejob

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/spf13/cobra"
)

func TestBindFlagsParsesIntoOverrides(t *testing.T) {
	var o horde.JobOverrides
	cmd := &cobra.Command{Use: "test"}
	BindFlags(cmd, &o)
	if err := cmd.Flags().Parse([]string{"--stream", "s", "--template", "t", "--change", "9", "--name", "n"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o != (horde.JobOverrides{StreamID: "s", TemplateID: "t", Change: 9, Name: "n"}) {
		t.Errorf("overrides = %+v", o)
	}
}
