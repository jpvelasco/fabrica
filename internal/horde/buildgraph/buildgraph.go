package buildgraph

import (
	"encoding/xml"
	"fmt"
	"os"
)

// BuildGraphJob holds the parsed essentials from a BuildGraph XML file.
type BuildGraphJob struct {
	Name   string
	Target string
}

type buildGraphXML struct {
	XMLName xml.Name   `xml:"BuildGraph"`
	Agents  []agentXML `xml:"Agent"`
}

type agentXML struct {
	Name  string    `xml:"Name,attr"`
	Nodes []nodeXML `xml:"Node"`
}

type nodeXML struct {
	Name string `xml:"Name,attr"`
}

// ParseBuildGraph reads path, parses the BuildGraph XML, and returns a
// BuildGraphJob. Returns an error if the file cannot be read or the XML
// is malformed.
func ParseBuildGraph(path string) (*BuildGraphJob, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading BuildGraph file %s: %w", path, err)
	}
	var bg buildGraphXML
	if err := xml.Unmarshal(data, &bg); err != nil {
		return nil, fmt.Errorf("parsing BuildGraph file %s: %w", path, err)
	}
	job := &BuildGraphJob{}
	if len(bg.Agents) > 0 {
		job.Name = bg.Agents[0].Name
		if len(bg.Agents[0].Nodes) > 0 {
			job.Target = bg.Agents[0].Nodes[0].Name
		}
	}
	return job, nil
}
