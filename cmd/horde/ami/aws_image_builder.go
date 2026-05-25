package ami

import (
	"encoding/json"
	"fmt"
)

// imageBuilderRecipe mirrors the structure of an EC2 Image Builder recipe document
// for validation and structured access. The canonical output is the rendered template.
type imageBuilderRecipe struct {
	Name                string               `json:"name"`
	Version             string               `json:"version"`
	Description         string               `json:"description"`
	ParentImage         string               `json:"parentImage"`
	BlockDeviceMappings []blockDeviceMapping `json:"blockDeviceMappings"`
	Components          []componentReference `json:"components"`
	Tags                map[string]string    `json:"tags"`
}

type blockDeviceMapping struct {
	DeviceName string  `json:"deviceName"`
	EBS        ebsSpec `json:"ebs"`
}

type ebsSpec struct {
	VolumeSize          int    `json:"volumeSize"`
	VolumeType          string `json:"volumeType"`
	DeleteOnTermination bool   `json:"deleteOnTermination"`
}

type componentReference struct {
	ComponentArn string `json:"componentArn"`
	Comment      string `json:"_comment,omitempty"`
}

// validateImageBuilderJSON parses rendered Image Builder JSON and returns a
// descriptive error if any required field is empty.
func validateImageBuilderJSON(data []byte) error {
	var r imageBuilderRecipe
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("invalid Image Builder JSON: %w", err)
	}
	if r.Name == "" {
		return fmt.Errorf("image builder recipe: name is required")
	}
	if r.ParentImage == "" {
		return fmt.Errorf("image builder recipe: parentImage is required")
	}
	if len(r.Components) == 0 {
		return fmt.Errorf("image builder recipe: at least one component is required")
	}
	return nil
}
