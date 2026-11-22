package buildgraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validBuildGraphXML = `<?xml version="1.0" encoding="utf-8"?>
<BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
    <Agent Name="BuildAgent" Type="Win64">
        <Node Name="Compile Editor Win64">
        </Node>
    </Agent>
</BuildGraph>`

func writeTempBuildGraph(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "BuildGraph.xml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseBuildGraphHappyPath(t *testing.T) {
	path := writeTempBuildGraph(t, validBuildGraphXML)
	job, err := ParseBuildGraph(path)
	if err != nil {
		t.Fatalf("ParseBuildGraph: %v", err)
	}
	if job == nil {
		t.Fatal("expected non-nil BuildGraphJob")
		return
	}
	if job.Name != "BuildAgent" {
		t.Errorf("job.Name = %q, want BuildAgent", job.Name)
	}
	if job.Target != "Compile Editor Win64" {
		t.Errorf("job.Target = %q, want 'Compile Editor Win64'", job.Target)
	}
}

func TestParseBuildGraphFileNotFound(t *testing.T) {
	_, err := ParseBuildGraph("/nonexistent/path/BuildGraph.xml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "reading BuildGraph file") {
		t.Errorf("error %q should mention 'reading BuildGraph file'", err.Error())
	}
}

func TestParseBuildGraphInvalidXML(t *testing.T) {
	path := writeTempBuildGraph(t, "<not valid xml><<</")
	_, err := ParseBuildGraph(path)
	if err == nil {
		t.Fatal("expected error for invalid XML")
	}
	if !strings.Contains(err.Error(), "parsing BuildGraph file") {
		t.Errorf("error %q should mention 'parsing BuildGraph file'", err.Error())
	}
}

func TestParseBuildGraphEmptyGraph(t *testing.T) {
	path := writeTempBuildGraph(t, `<?xml version="1.0"?><BuildGraph xmlns="http://www.epicgames.com/BuildGraph"></BuildGraph>`)
	job, err := ParseBuildGraph(path)
	if err != nil {
		t.Fatalf("empty BuildGraph should not error: %v", err)
	}
	if job.Name != "" {
		t.Errorf("empty graph: job.Name = %q, want empty", job.Name)
	}
	if job.Target != "" {
		t.Errorf("empty graph: job.Target = %q, want empty", job.Target)
	}
}

func TestParseBuildGraphMultipleAgentsUsesFirst(t *testing.T) {
	xml := `<?xml version="1.0"?>
<BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
    <Agent Name="FirstAgent" Type="Win64"><Node Name="FirstNode"/></Agent>
    <Agent Name="SecondAgent" Type="Win64"><Node Name="SecondNode"/></Agent>
</BuildGraph>`
	path := writeTempBuildGraph(t, xml)
	job, err := ParseBuildGraph(path)
	if err != nil {
		t.Fatalf("ParseBuildGraph: %v", err)
	}
	if job.Name != "FirstAgent" {
		t.Errorf("job.Name = %q, want FirstAgent", job.Name)
	}
}

func TestParseBuildGraphAgentWithNoNodes(t *testing.T) {
	xml := `<?xml version="1.0"?>
<BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
    <Agent Name="EmptyAgent" Type="Win64"></Agent>
</BuildGraph>`
	path := writeTempBuildGraph(t, xml)
	job, err := ParseBuildGraph(path)
	if err != nil {
		t.Fatalf("ParseBuildGraph: %v", err)
	}
	if job.Name != "EmptyAgent" {
		t.Errorf("job.Name = %q, want EmptyAgent", job.Name)
	}
	if job.Target != "" {
		t.Errorf("job.Target = %q, want empty for agent with no nodes", job.Target)
	}
}

// TestValidateJobValidTarget verifies that a job with a non-empty target passes validation.
func TestValidateJobValidTarget(t *testing.T) {
	job := &BuildGraphJob{Name: "BuildAgent", Target: "Compile UnrealGame Win64"}
	if err := ValidateJob(job); err != nil {
		t.Fatalf("ValidateJob: %v", err)
	}
}

// TestValidateJobEmptyTarget verifies that an empty target returns an actionable error.
func TestValidateJobEmptyTarget(t *testing.T) {
	job := &BuildGraphJob{Name: "EmptyAgent", Target: ""}
	err := ValidateJob(job)
	if err == nil {
		t.Fatal("expected error for empty target")
	}
	if !strings.Contains(err.Error(), "no build target") {
		t.Errorf("error %q should mention 'no build target'", err.Error())
	}
	if !strings.Contains(err.Error(), "BuildGraph.sample.xml") {
		t.Errorf("error %q should reference sample file", err.Error())
	}
}

// TestValidateJobNilJob verifies that a nil job returns an error.
func TestValidateJobNilJob(t *testing.T) {
	err := ValidateJob(nil)
	if err == nil {
		t.Fatal("expected error for nil job")
	}
}

// TestParseBuildGraphSampleFile verifies that the sample BuildGraph file
// in examples/ resolves a valid target end-to-end.
func TestParseBuildGraphSampleFile(t *testing.T) {
	job, err := ParseBuildGraph("../../../examples/BuildGraph.sample.xml")
	if err != nil {
		t.Fatalf("ParseBuildGraph sample: %v", err)
	}
	if job.Name == "" {
		t.Error("sample file: job.Name is empty")
	}
	if job.Target == "" {
		t.Error("sample file: job.Target is empty — the sample is invalid")
	}
	if err := ValidateJob(job); err != nil {
		t.Fatalf("ValidateJob sample: %v", err)
	}
}

const aggregateBuildGraphXML = `<?xml version="1.0"?>
<BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
    <Agent Name="BuildAgent" Type="Win64">
        <Node Name="Compile Editor Win64"/>
        <Node Name="Cook Game"/>
    </Agent>
    <Aggregate Name="SmokeTest" Requires="Compile Editor Win64;Cook Game"/>
    <Aggregate Name="Nightly" Requires="Cook Game"/>
</BuildGraph>`

func parseTestJob(t *testing.T, content string) *BuildGraphJob {
	t.Helper()
	job, err := ParseBuildGraph(writeTempBuildGraph(t, content))
	if err != nil {
		t.Fatalf("ParseBuildGraph: %v", err)
	}
	return job
}

// TestParseBuildGraphAggregatesCollected verifies <Aggregate> elements are parsed in document order.
func TestParseBuildGraphAggregatesCollected(t *testing.T) {
	job := parseTestJob(t, aggregateBuildGraphXML)
	want := []string{"SmokeTest", "Nightly"}
	if len(job.aggregates) != len(want) || job.aggregates[0] != want[0] || job.aggregates[1] != want[1] {
		t.Errorf("aggregates = %v, want %v", job.aggregates, want)
	}
}

// TestResolveTarget is the table test for target selection precedence:
// explicit override > first <Aggregate> > first <Node> of first <Agent>.
func TestResolveTarget(t *testing.T) {
	tests := []struct {
		name        string
		xml         string
		override    string
		wantTarget  string
		wantReason  string
		wantWarning bool
	}{
		{
			name:       "aggregate preferred over node",
			xml:        aggregateBuildGraphXML,
			wantTarget: "SmokeTest",
			wantReason: "first <Aggregate>",
		},
		{
			name:       "node fallback when no aggregate",
			xml:        validBuildGraphXML,
			wantTarget: "Compile Editor Win64",
			wantReason: "first <Node> of first <Agent>",
		},
		{
			name:       "override selects an aggregate",
			xml:        aggregateBuildGraphXML,
			override:   "Nightly",
			wantTarget: "Nightly",
			wantReason: "--target",
		},
		{
			name:       "override selects a node",
			xml:        aggregateBuildGraphXML,
			override:   "Cook Game",
			wantTarget: "Cook Game",
			wantReason: "--target",
		},
		{
			name:        "override not found warns but is used",
			xml:         aggregateBuildGraphXML,
			override:    "FromInclude",
			wantTarget:  "FromInclude",
			wantReason:  "--target",
			wantWarning: true,
		},
		{
			name:        "override on empty graph warns but is used",
			xml:         `<BuildGraph xmlns="http://www.epicgames.com/BuildGraph"></BuildGraph>`,
			override:    "Anything",
			wantTarget:  "Anything",
			wantReason:  "--target",
			wantWarning: true,
		},
		{
			name: "empty graph without override yields no target",
			xml:  `<BuildGraph xmlns="http://www.epicgames.com/BuildGraph"></BuildGraph>`,
		},
		{
			name: "agent without nodes and no aggregate yields no target",
			xml:  `<BuildGraph><Agent Name="Empty" Type="Win64"></Agent></BuildGraph>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := parseTestJob(t, tt.xml)
			choice := ResolveTarget(job, tt.override)
			if choice.Name != tt.wantTarget {
				t.Errorf("Name = %q, want %q", choice.Name, tt.wantTarget)
			}
			if job.Target != tt.wantTarget {
				t.Errorf("job.Target = %q, want %q", job.Target, tt.wantTarget)
			}
			if choice.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", choice.Reason, tt.wantReason)
			}
			if (choice.Warning != "") != tt.wantWarning {
				t.Errorf("Warning = %q, wantWarning = %v", choice.Warning, tt.wantWarning)
			}
		})
	}
}

// TestResolveTargetWarningNamesOverride verifies the warning names the missing target and the likely cause.
func TestResolveTargetWarningNamesOverride(t *testing.T) {
	job := parseTestJob(t, aggregateBuildGraphXML)
	choice := ResolveTarget(job, "FromInclude")
	for _, want := range []string{`"FromInclude"`, "<Do If>"} {
		if !strings.Contains(choice.Warning, want) {
			t.Errorf("warning %q should contain %q", choice.Warning, want)
		}
	}
}

// TestValidateJobNoTargetMentionsAggregate verifies the empty-target error says what to add.
func TestValidateJobNoTargetMentionsAggregate(t *testing.T) {
	err := ValidateJob(parseTestJob(t, `<BuildGraph xmlns="http://www.epicgames.com/BuildGraph"></BuildGraph>`))
	if err == nil {
		t.Fatal("expected error for graph with no targets")
	}
	if !strings.Contains(err.Error(), "<Aggregate>") {
		t.Errorf("error %q should tell the user to add an <Aggregate>", err.Error())
	}
}
