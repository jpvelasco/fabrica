package ami

import (
	"encoding/json"
	"fmt"
	"strings"
)

// imageBuilderRecipe mirrors the structure of an EC2 Image Builder recipe document
// for validation. The canonical output is the rendered template.
type imageBuilderRecipe struct {
	Name            string               `json:"name"`
	SemanticVersion string               `json:"semanticVersion"`
	Description     string               `json:"description"`
	ParentImage     string               `json:"parentImage"`
	Components      []componentReference `json:"components"`
	Tags            map[string]string    `json:"tags"`
}

type componentReference struct {
	ComponentArn string `json:"componentArn"`
}

// validateComponentYAML checks that a rendered Image Builder Component document
// has the required top-level fields, and — for a docker install — that the bake
// actually writes the compose stack and pulls the server image. Placeholders
// like REPLACE_WITH_YOUR_BUCKET / REPLACE_WITH_ECR_REPOSITORY are intentional —
// users substitute them before uploading to AWS.
func validateComponentYAML(data []byte, install string) error {
	for _, required := range []string{"schemaVersion:", "phases:", "name:"} {
		found := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, required) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("component YAML is missing required top-level field %q", required)
		}
	}
	// A docker install must bake a jobs-capable stack: write the compose file
	// + configs, pull/tag the server image, and fail the build if the compose
	// file ends up missing. Without these the generated AMI 404s on the jobs
	// API (the #459 failure), so the generator refuses to emit a component
	// that lacks them.
	if install == "docker" {
		for _, marker := range dockerComponentMarkers {
			if !strings.Contains(string(data), marker) {
				return fmt.Errorf("docker component is missing the %q step required to bake a jobs-capable stack", marker)
			}
		}
	}
	return nil
}

// dockerComponentMarkers are the shell markers a docker-install component must
// contain to bake a jobs-capable AMI. Each maps to a step that, if absent,
// leaves the generated AMI unable to serve the jobs API.
var dockerComponentMarkers = []string{
	"/etc/horde/docker-compose.yml",         // compose file is written where the unit + cloud-init look
	"docker pull",                           // the server image is pulled into the AMI
	"docker tag",                            // the pulled image is tagged for the compose stack
	"test -s /etc/horde/docker-compose.yml", // bake-time gate fails closed if compose is missing
}

// validateImageBuilderJSON parses rendered Image Builder JSON and returns a
// descriptive error if any required field is missing or malformed. The
// "REPLACE_WITH_CUSTOM_COMPONENT_ARN" placeholder is allowed at this stage
// since the user is expected to substitute it before submitting to AWS.
func validateImageBuilderJSON(data []byte) error {
	var r imageBuilderRecipe
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("invalid Image Builder JSON: %w", err)
	}
	if r.Name == "" {
		return fmt.Errorf("name is required")
	}
	if r.SemanticVersion == "" {
		return fmt.Errorf("semanticVersion is required")
	}
	if r.ParentImage == "" {
		return fmt.Errorf("parentImage is required")
	}
	if len(r.Components) == 0 {
		return fmt.Errorf("at least one component is required")
	}
	for i, c := range r.Components {
		if c.ComponentArn == "" {
			return fmt.Errorf("components[%d].componentArn is empty", i)
		}
		if c.ComponentArn == "REPLACE_WITH_CUSTOM_COMPONENT_ARN" {
			continue
		}
		if !strings.HasPrefix(c.ComponentArn, "arn:aws:imagebuilder:") {
			return fmt.Errorf("components[%d].componentArn must be an Image Builder ARN, got %q", i, c.ComponentArn)
		}
	}
	return nil
}
