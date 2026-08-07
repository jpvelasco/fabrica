package mcp

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/costsource"
	"github.com/jpvelasco/fabrica/cmd/internal/doctorchecks"
	"github.com/jpvelasco/fabrica/cmd/internal/statusreport"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/drift"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	fabricav "github.com/jpvelasco/fabrica/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.yaml.in/yaml/v3"
)

// redactKeys is the denylist of suffixes for config field names that should be
// redacted. Matching is case-insensitive ends-with.
var redactKeys = []string{"password", "token", "secret", "api_key", "access_key"}

// Result types

type VersionResult struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

type DoctorResult struct {
	Checks  []DoctorCheck `json:"checks"`
	Healthy bool          `json:"healthy"`
}

type DoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type StatusResult struct {
	Backend statusreport.StatusBackend  `json:"backend"`
	Modules []statusreport.StatusModule `json:"modules"`
	Summary statusreport.StatusSummary  `json:"summary"`
}

type DriftResult struct {
	Report *drift.DriftReport `json:"report"`
}

type CostReportResult struct {
	Total      float64      `json:"total"`
	Confidence string       `json:"confidence"`
	Modules    []CostModule `json:"modules"`
}

type CostModule struct {
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Subtotal float64 `json:"subtotal"`
	Note     string  `json:"note,omitempty"`
}

type ConfigShowResult struct {
	Config     map[string]any `json:"config"`
	ConfigPath string         `json:"configPath"`
}

func registerTools(s *mcp.Server, rt globals.Runtime) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "fabrica_version",
		Description: "Show Fabrica version, commit, Go runtime, and platform",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, VersionResult, error) {
		return nil, VersionResult{
			Version: fabricav.Version,
			Commit:  fabricav.Commit,
			Go:      runtime.Version(),
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "fabrica_doctor",
		Description: "Check environment health: Go, credentials, region, state backend",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, DoctorResult, error) {
		backend, _ := rt.Provider.(cloud.StateBackendChecker)
		checks := doctorchecks.RunChecks(ctx, rt, backend)

		healthy := true
		for _, c := range checks {
			if c.Status == "fail" {
				healthy = false
				break
			}
		}

		var dc []DoctorCheck
		for _, c := range checks {
			dc = append(dc, DoctorCheck{
				Name:    c.Name,
				Status:  c.Status,
				Message: c.Message,
			})
		}

		return nil, DoctorResult{
			Checks:  dc,
			Healthy: healthy,
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "fabrica_status",
		Description: "Show aggregate status of all provisioned modules and state backend",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, StatusResult, error) {
		st, err := fabricastate.ReadStateOrNew(rt.Config.Cloud.AWS.AccountID, rt.Config.Cloud.AWS.Region)
		if err != nil {
			return nil, StatusResult{}, fmt.Errorf("reading state: %w", err)
		}

		report := statusreport.BuildStatusReport(ctx, st, rt, statusreport.BuildOptions{})

		return nil, StatusResult{
			Backend: report.Backend,
			Modules: report.Modules,
			Summary: report.Summary,
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "fabrica_drift",
		Description: "Detect drift between recorded state and live AWS resources",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, DriftResult, error) {
		if rt.Provider == nil {
			return nil, DriftResult{}, fmt.Errorf("provider not available — check your AWS credentials")
		}

		st, err := fabricastate.ReadStateOrNew(rt.Config.Cloud.AWS.AccountID, rt.Config.Cloud.AWS.Region)
		if err != nil {
			return nil, DriftResult{}, fmt.Errorf("reading state: %w", err)
		}

		engine := &drift.Engine{
			State:           st,
			ResourceGet:     rt.Provider.Resources().Get,
			ResourceList:    rt.Provider.Resources().List,
			BackendChecker:  nil,
			CodeBuildRunner: nil,
			Config: &drift.DriftConfig{
				Account: rt.Config.Cloud.AWS.AccountID,
				Region:  rt.Config.Cloud.AWS.Region,
				Bucket:  rt.Config.State.Bucket,
				Table:   rt.Config.State.Table,
			},
		}
		if bc, ok := rt.Provider.(cloud.StateBackendChecker); ok {
			engine.BackendChecker = bc
		}
		if cb, ok := rt.Provider.(cloud.CodeBuildRunner); ok {
			engine.CodeBuildRunner = cb
		}

		report := engine.Run(ctx)
		return nil, DriftResult{Report: report}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "fabrica_cost_report",
		Description: "Show estimated monthly cost broken down by module (offline, no AWS calls)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, CostReportResult, error) {
		st, err := fabricastate.ReadStateOrNew(rt.Config.Cloud.AWS.AccountID, rt.Config.Cloud.AWS.Region)
		if err != nil {
			return nil, CostReportResult{}, fmt.Errorf("reading state: %w", err)
		}

		b := costsource.Aggregate(rt.Config, st, cost.Global)

		var modules []CostModule
		for _, r := range b.Modules {
			modules = append(modules, CostModule{
				Name:     r.Name,
				Status:   r.Status,
				Subtotal: r.Subtotal,
				Note:     r.Note,
			})
		}

		return nil, CostReportResult{
			Total:      b.Total,
			Confidence: b.Confidence.String(),
			Modules:    modules,
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "fabrica_config_show",
		Description: "Show current configuration with sensitive fields redacted",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, ConfigShowResult, error) {
		yamlBytes, err := rt.Config.YAML()
		if err != nil {
			return nil, ConfigShowResult{}, fmt.Errorf("reading config: %w", err)
		}

		var raw map[string]any
		if err := yaml.Unmarshal(yamlBytes, &raw); err != nil {
			return nil, ConfigShowResult{}, fmt.Errorf("parsing config: %w", err)
		}

		redactMap(raw)

		return nil, ConfigShowResult{
			Config:     raw,
			ConfigPath: rt.ConfigFile(),
		}, nil
	})
}

// redactMap recursively walks a map and redacts any key whose name ends with
// a sensitive suffix (case-insensitive).
func redactMap(m map[string]any) {
	for k, v := range m {
		if shouldRedact(k) {
			m[k] = "[redacted]"
			continue
		}
		switch nested := v.(type) {
		case map[string]any:
			redactMap(nested)
		case []any:
			for _, item := range nested {
				if mm, ok := item.(map[string]any); ok {
					redactMap(mm)
				}
			}
		}
	}
}

func shouldRedact(key string) bool {
	lower := strings.ToLower(key)
	for _, suffix := range redactKeys {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}
