package logs_test

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci/logs"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
)

// TestLogsCobraWiring exercises New(): a provider without CodeBuildRunner must
// produce a clean error (not a panic) through the full Cobra execution path.
func TestLogsCobraWiring(t *testing.T) {
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	src := testutil.NewTestRuntime(&testutil.CobraFakeProvider{})
	root.AddCommand(logs.New(src, optionsSource, &out))
	if _, err := testutil.RunCommandWithOut(t, root, &out, "logs", "build-1"); err == nil {
		t.Fatal("expected error: provider lacks CodeBuildRunner")
	}
}

// TestLogsRequiresBuildID verifies the ExactArgs(1) constraint.
func TestLogsRequiresBuildID(t *testing.T) {
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	src := testutil.NewNilProviderRuntime()
	root.AddCommand(logs.New(src, optionsSource, &out))
	if _, err := testutil.RunCommandWithOut(t, root, &out, "logs"); err == nil {
		t.Fatal("expected error: build-id argument required")
	}
}
