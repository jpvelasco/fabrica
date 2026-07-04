package e2e

import "testing"

// TestJSONContract: --json output for status and cost report parses and carries
// the expected top-level fields.
func TestJSONContract(t *testing.T) {
	setupE2E(t)
	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "perforce", "create", "--yes"); err != nil {
		t.Fatalf("perforce create: %v\n%s", err, out)
	}

	// status --json → { backend, modules, summary }
	out, err := runCLI(t, "--json", "status")
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, out)
	}
	var status struct {
		Backend map[string]any   `json:"backend"`
		Modules []map[string]any `json:"modules"`
		Summary map[string]any   `json:"summary"`
	}
	assertJSON(t, out, &status)
	if len(status.Modules) == 0 {
		t.Fatalf("status --json: expected at least one module\n%s", out)
	}

	// cost report --json → { total, confidence, modules, note }
	out, err = runCLI(t, "--json", "cost", "report")
	if err != nil {
		t.Fatalf("cost report --json: %v\n%s", err, out)
	}
	var cost struct {
		Total      float64          `json:"total"`
		Confidence string           `json:"confidence"`
		Modules    []map[string]any `json:"modules"`
		Note       string           `json:"note"`
	}
	assertJSON(t, out, &cost)
	if len(cost.Modules) == 0 {
		t.Fatalf("cost report --json: expected at least one module\n%s", out)
	}
}
