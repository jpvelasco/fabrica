package root_test

import (
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/root"
)

func TestExecuteVersion(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"fabrica", "version"}
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}
