package submit_test

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
)

// TestSubmitCobraNoFileDryRun verifies the BuildGraph file is optional and that
// the flags reach the previewed request, overriding the config values.
func TestSubmitCobraNoFileDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, submitProvisionedStateJSON())

	got, err := runSubmit(t, newCobraRuntime(submitDryRunProvider()), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "template default")
	testutil.AssertContains(t, got, `"templateId":"template-compile"`)

	got, err = runSubmit(t, newCobraRuntime(submitDryRunProvider()), "--dry-run",
		"--stream", "flag-stream", "--template", "flag-template", "--change", "88", "--name", "nightly", writeTempBuildGraph(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, `"streamId":"flag-stream"`)
	testutil.AssertContains(t, got, `"templateId":"flag-template"`)
	testutil.AssertContains(t, got, `"commitId":"88"`)
	testutil.AssertContains(t, got, `"name":"nightly"`)
	testutil.AssertContains(t, got, "not uploaded")
	if strings.Contains(got, `"arguments"`) {
		t.Errorf("request must never carry arguments; output:\n%s", got)
	}
}
