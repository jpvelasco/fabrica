package provision

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestRunCreate_PreflightBlocksBeforeState verifies a preflight error stops both
// the dry run and create before any state access.
func TestRunCreate_PreflightBlocksBeforeState(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		var events []string
		spec := workflowTestSpec(&events)
		spec.DryRun = dryRun
		spec.Preflight = func(context.Context) ([]string, error) { return nil, errors.New("blocked by BPA") }

		err := RunCreate(context.Background(), spec)
		if err == nil || !strings.Contains(err.Error(), "blocked by BPA") {
			t.Fatalf("dryRun=%v: RunCreate() error = %v, want the preflight error", dryRun, err)
		}
		if len(events) != 0 {
			t.Fatalf("dryRun=%v: events = %v, want none after a preflight block", dryRun, events)
		}
	}
}

// TestRunCreate_PreflightWarningsReachPrinters verifies warnings are handed to
// the dry-run printer and printed ahead of the plan on create.
func TestRunCreate_PreflightWarningsReachPrinters(t *testing.T) {
	warn := func(context.Context) ([]string, error) { return []string{"careful"}, nil }

	var got []string
	dry := workflowTestSpec(new([]string))
	dry.DryRun = true
	dry.Preflight = warn
	dry.PrintDryRun = func(_ workflowTestPlan, w []string) { got = w }
	if err := RunCreate(context.Background(), dry); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"careful"}) {
		t.Fatalf("dry-run warnings = %v, want [careful]", got)
	}

	var events []string
	create := workflowTestSpec(&events)
	create.Preflight = warn
	if err := RunCreate(context.Background(), create); err != nil {
		t.Fatalf("create: %v", err)
	}
	if out := create.Out.(*bytes.Buffer).String(); !strings.Contains(out, "Warning: careful") {
		t.Fatalf("create output = %q, want the warning", out)
	}
}
