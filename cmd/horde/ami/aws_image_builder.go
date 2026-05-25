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
