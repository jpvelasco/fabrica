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
