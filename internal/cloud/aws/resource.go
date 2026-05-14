package aws

import (
	"context"
	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// resource describes an AWS Cloud Control resource.
type resource struct {
	TypeName     string          `json:"typeName"`
	Identifier   string          `json:"identifier"`
	DesiredState json.RawMessage `json:"desiredState,omitempty"`
	ActualState  json.RawMessage `json:"actualState,omitempty"`
	Type         string          `json:"type,omitempty"`
}

// resourceList is the response shape for ListResources.
type resourceList struct {
	ResourceDescriptions []*resource `json:"resourceDescriptions,omitempty"`
	NextToken            string      `json:"nextToken,omitempty"`
}

// resourceRequest is the response shape for CreateResource, UpdateResource.
type resourceRequest struct {
	RequestToken string `json:"requestToken"`
}

// resourceStatus describes the status of an async request.
type resourceStatus struct {
	RequestToken string               `json:"requestToken"`
	Status       string               `json:"status"`
	Error        *resourceStatusError `json:"error,omitempty"`
}

type resourceStatusError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ccClient wraps the Cloud Control API.
type ccClient struct {
	cfg   aws.Config
	loaded bool
}

// ensureConfig loads the AWS config for this client if not already loaded.
func (c *ccClient) ensureConfig(ctx context.Context, ac awsConfig) error {
	if c.loaded {
		return nil
	}
	cfg, err := loadAWSConfig(ctx, ac.region, ac.profile)
	if err != nil {
		return err
	}
	c.cfg = cfg
	c.loaded = true
	return nil
}
