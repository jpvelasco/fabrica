package buildgraph

import (
	"encoding/xml"
	"fmt"
	"os"
	"slices"
)

// BuildGraphJob holds the parsed essentials from a BuildGraph XML file.
// Target is the resolved default after ParseBuildGraph; ResolveTarget
// replaces it when an explicit override is given.
type BuildGraphJob struct {
	Name   string
	Target string

	aggregates     []string
	nodes          []string
	firstAgentNode string
}

// TargetChoice is the resolved build target and why it was chosen.
type TargetChoice struct {
	Name   string
	Reason string
	// Warning is set when an explicit override matches no parsed <Aggregate>
	// or <Node> name. The target is still used: includes and <Do If> blocks
	// hide nodes from this parser.
	Warning string
}

type buildGraphXML struct {
	XMLName    xml.Name       `xml:"BuildGraph"`
	Agents     []agentXML     `xml:"Agent"`
	Aggregates []aggregateXML `xml:"Aggregate"`
}

type agentXML struct {
	Name  string    `xml:"Name,attr"`
	Nodes []nodeXML `xml:"Node"`
}

type nodeXML struct {
	Name string `xml:"Name,attr"`
}

type aggregateXML struct {
	Name     string `xml:"Name,attr"`
	Requires string `xml:"Requires,attr"`
}

// ParseBuildGraph reads path, parses the BuildGraph XML, and returns a
// BuildGraphJob with its default Target resolved (no override). Returns an
// error if the file cannot be read or the XML is malformed.
func ParseBuildGraph(path string) (*BuildGraphJob, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading BuildGraph file %s: %w", path, err)
	}
	var bg buildGraphXML
	if err := xml.Unmarshal(data, &bg); err != nil {
		return nil, fmt.Errorf("parsing BuildGraph file %s: %w", path, err)
	}
	job := jobFromXML(&bg)
	ResolveTarget(job, "")
	return job, nil
}

func jobFromXML(bg *buildGraphXML) *BuildGraphJob {
	job := &BuildGraphJob{}
	for _, a := range bg.Aggregates {
		job.aggregates = append(job.aggregates, a.Name)
	}
	for _, agent := range bg.Agents {
		for _, n := range agent.Nodes {
			job.nodes = append(job.nodes, n.Name)
		}
	}
	if len(bg.Agents) > 0 {
		job.Name = bg.Agents[0].Name
		if len(bg.Agents[0].Nodes) > 0 {
			job.firstAgentNode = bg.Agents[0].Nodes[0].Name
		}
	}
	return job
}

// ResolveTarget picks the build target and stores it in job.Target. Order:
// the explicit override, then the first <Aggregate>, then the first <Node>
// of the first <Agent>. An empty override is not an error; a graph with no
// candidate leaves Target empty for ValidateJob to report.
func ResolveTarget(job *BuildGraphJob, override string) TargetChoice {
	choice := defaultChoice(job)
	if override != "" {
		choice = overrideChoice(job, override)
	}
	job.Target = choice.Name
	return choice
}

func defaultChoice(job *BuildGraphJob) TargetChoice {
	switch {
	case len(job.aggregates) > 0:
		return TargetChoice{Name: job.aggregates[0], Reason: "first <Aggregate>"}
	case job.firstAgentNode != "":
		return TargetChoice{Name: job.firstAgentNode, Reason: "first <Node> of first <Agent>"}
	}
	return TargetChoice{}
}

func overrideChoice(job *BuildGraphJob, override string) TargetChoice {
	choice := TargetChoice{Name: override, Reason: "--target"}
	if !slices.Contains(job.aggregates, override) && !slices.Contains(job.nodes, override) {
		choice.Warning = fmt.Sprintf("target %q was not found among the <Aggregate> or <Node> names in this file; "+
			"it may come from an included file or a <Do If> block, so it will be used as given", override)
	}
	return choice
}

// ValidateJob checks that the parsed job has a non-empty target. An empty
// target means the BuildGraph XML has neither an <Aggregate> nor a <Node>
// under the first <Agent> for the parser to resolve — this produces a
// PRE_BUILD failure downstream if not caught here. Returns an actionable
// error pointing to the sample file.
func ValidateJob(job *BuildGraphJob) error {
	if job == nil || job.Target == "" {
		return fmt.Errorf("BuildGraph has no build target: add an <Aggregate> (preferred), or a <Node> with a Name attribute under the first <Agent>\n\n" +
			"Use examples/BuildGraph.sample.xml as a reference, or fix your BuildGraph XML")
	}
	return nil
}
