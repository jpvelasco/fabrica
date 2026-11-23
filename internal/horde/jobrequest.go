package horde

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/jpvelasco/fabrica/internal/config"
)

// Job states reported by GET /api/v1/jobs/{id} on Horde 5.8.
const (
	JobStateWaiting  = "Waiting"
	JobStateRunning  = "Running"
	JobStateComplete = "Complete"
)

// CreateJobRequest is the body of POST /api/v1/jobs on Horde 5.8. Targets
// replaces only the template's -Target= arguments. There is deliberately no
// arguments field: Horde's arguments replace the template's whole list.
type CreateJobRequest struct {
	StreamID   string   `json:"streamId"`
	TemplateID string   `json:"templateId"`
	Name       string   `json:"name,omitempty"`
	CommitID   string   `json:"commitId,omitempty"`
	Targets    []string `json:"targets,omitempty"`
}

// JobOverrides are per-invocation values that win over horde.* config. Zero
// values mean "not set".
type JobOverrides struct {
	StreamID   string
	TemplateID string
	Change     int
	Name       string
	Target     string
}

// NewCreateJobRequest builds a request from config and overrides. A zero change
// is omitted so Horde picks the latest; a negative one is kept so Validate
// rejects it.
func NewCreateJobRequest(cfg config.HordeConfig, o JobOverrides) CreateJobRequest {
	req := CreateJobRequest{
		StreamID:   firstNonEmpty(o.StreamID, cfg.StreamID),
		TemplateID: firstNonEmpty(o.TemplateID, cfg.TemplateID),
		Name:       o.Name,
	}
	change := cfg.Change
	if o.Change != 0 {
		change = o.Change
	}
	if change != 0 {
		req.CommitID = strconv.Itoa(change)
	}
	if o.Target != "" {
		req.Targets = []string{o.Target}
	}
	return req
}

// Validate reports whether the request can be sent.
func (r CreateJobRequest) Validate() error {
	if r.StreamID == "" || r.TemplateID == "" {
		return fmt.Errorf("Horde jobs need a stream and template: set horde.streamId/horde.templateId (or --stream/--template); both must exist in the coordinator's Horde config, see docs/horde-jobs.md")
	}
	if strings.IndexFunc(r.StreamID, unicode.IsSpace) >= 0 {
		return fmt.Errorf("Horde stream ID %q contains whitespace: use the stream ID exactly as it appears in the coordinator's globals.json", r.StreamID)
	}
	if strings.IndexFunc(r.TemplateID, unicode.IsSpace) >= 0 {
		return fmt.Errorf("Horde template ID %q contains whitespace: use the template ID exactly as it appears in the coordinator's globals.json", r.TemplateID)
	}
	if r.CommitID != "" {
		if n, err := strconv.Atoi(r.CommitID); err != nil || n <= 0 {
			return fmt.Errorf("change %s is not a positive Perforce changelist number: set --change or horde.change to a changelist, or leave both unset for Horde's latest", r.CommitID)
		}
	}
	return nil
}

func firstNonEmpty(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}
