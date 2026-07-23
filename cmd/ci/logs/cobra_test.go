package logs_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci/logs"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
)

// TestLogsCobraWiring exercises New(): a provider without CodeBuildRunner must
// produce a clean error (not a panic) through the full Cobra execution path.
func TestLogsCobraWiring(t *testing.T) {
	var out bytes.Buffer
	root, opts := testutil.BuildTestRoot(&out)
	optionsSource := func() globals.Options { return *opts }
	src := testutil.NewTestRuntime(&testutil.CobraFakeProvider{})
	root.AddCommand(logs.New(src, optionsSource, &out))
	root.SetArgs([]string{"logs", "build-1"})

	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error: provider lacks CodeBuildRunner")
	}
}

// TestLogsRequiresBuildID verifies the ExactArgs(1) constraint.
func TestLogsRequiresBuildID(t *testing.T) {
	var out bytes.Buffer
	root, opts := testutil.BuildTestRoot(&out)
	optionsSource := func() globals.Options { return *opts }
	src := testutil.NewNilProviderRuntime()
	root.AddCommand(logs.New(src, optionsSource, &out))
	root.SetArgs([]string{"logs"})

	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error: build-id argument required")
	}
}
