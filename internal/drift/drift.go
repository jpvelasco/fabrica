// Package drift provides drift detection for Fabrica-managed resources. It
// compares recorded state (.fabrica/state.json) against live AWS resources and
// reports whether each resource is in sync, missing, or has attribute
// mismatches.
//
// The engine is provider-agnostic: it accepts a state snapshot and provider
// interfaces (ResourceClient, StateBackendChecker, CodeBuildRunner) and produces
// a DriftReport. No AWS SDK imports belong here.
//
// TODO: Extra (live-not-in-state) detection requires Cloud Control List-based
// discovery — tagging conventions, VPC scoping, and a separate scan pass.
// Deferred to a follow-up.
package drift

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
)

// DriftStatus represents the drift state of a single resource.
type DriftStatus string

const (
	InSync   DriftStatus = "inSync"
	Missing  DriftStatus = "missing"
	Mismatch DriftStatus = "mismatch"
	Error    DriftStatus = "error"
)

// DriftResult is the drift status of one resource.
type DriftResult struct {
	Module     string      `json:"module"`
	TypeName   string      `json:"typeName"`
	Identifier string      `json:"identifier"`
	Status     DriftStatus `json:"status"`
	Details    string      `json:"details,omitempty"`
}

// DriftReport is the aggregate drift report for all modules.
type DriftReport struct {
	Backend  DriftBackend  `json:"backend"`
	Modules  []ModuleDrift `json:"modules"`
	Checked  int           `json:"checked"`
	InSync   int           `json:"inSync"`
	Missing  int           `json:"missing"`
	Mismatch int           `json:"mismatch"`
	Errors   int           `json:"errors"`
}

// DriftBackend reports the drift status of the state backend.
type DriftBackend struct {
	Bucket        string      `json:"bucket,omitempty"`
	BucketStatus  DriftStatus `json:"bucketStatus"`
	BucketDetails string      `json:"bucketDetails,omitempty"`
	Table         string      `json:"table,omitempty"`
	TableStatus   DriftStatus `json:"tableStatus"`
	TableDetails  string      `json:"tableDetails,omitempty"`
}

// ModuleDrift is the per-module drift section.
type ModuleDrift struct {
	Name      string        `json:"name"`
	Resources []DriftResult `json:"resources"`
}

// Engine is the drift detection engine. All fields are seams for testability.
type Engine struct {
	State           *state.State
	ResourceGet     func(ctx context.Context, r *cloud.Resource) error
	BackendChecker  cloud.StateBackendChecker
	CodeBuildRunner cloud.CodeBuildRunner
	Config          *DriftConfig
}

// DriftConfig holds configuration for the drift check.
type DriftConfig struct {
	Account string
	Region  string
	Bucket  string
	Table   string
}

// Run performs drift detection across all modules in the state and the
// state backend, returning a DriftReport.
func (e *Engine) Run(ctx context.Context) *DriftReport {
	report := &DriftReport{}

	// Check state backend health.
	report.Backend = e.checkBackend(ctx)

	// Check each module's resources.
	for i := range e.State.Modules {
		md := e.checkModule(ctx, &e.State.Modules[i])
		report.Modules = append(report.Modules, md)
	}

	// Compute summary counts.
	for i := range report.Modules {
		for j := range report.Modules[i].Resources {
			r := &report.Modules[i].Resources[j]
			report.Checked++
			switch r.Status {
			case InSync:
				report.InSync++
			case Missing:
				report.Missing++
			case Mismatch:
				report.Mismatch++
			case Error:
				report.Errors++
			}
		}
	}

	return report
}

func (e *Engine) checkBackend(ctx context.Context) DriftBackend {
	b := DriftBackend{
		BucketStatus: Error,
		TableStatus:  Error,
	}

	if e.Config == nil {
		b.BucketDetails = "no drift config available"
		b.TableDetails = "no drift config available"
		return b
	}

	b.Bucket = e.Config.Bucket
	b.Table = e.Config.Table

	if e.BackendChecker == nil {
		b.BucketDetails = "no backend checker available"
		b.TableDetails = "no backend checker available"
		return b
	}

	if e.Config.Bucket != "" {
		exists, err := e.BackendChecker.StateBucketExists(ctx, e.Config.Bucket)
		switch {
		case err != nil:
			b.BucketStatus = Error
			b.BucketDetails = fmt.Sprintf("check failed: %v", err)
		case exists:
			b.BucketStatus = InSync
		default:
			b.BucketStatus = Missing
			b.BucketDetails = "state bucket not found"
		}
	}

	if e.Config.Table != "" {
		exists, err := e.BackendChecker.StateLockTableExists(ctx, e.Config.Table)
		switch {
		case err != nil:
			b.TableStatus = Error
			b.TableDetails = fmt.Sprintf("check failed: %v", err)
		case exists:
			b.TableStatus = InSync
		default:
			b.TableStatus = Missing
			b.TableDetails = "lock table not found"
		}
	}

	return b
}

func (e *Engine) checkModule(ctx context.Context, m *state.ModuleState) ModuleDrift {
	md := ModuleDrift{Name: m.Name}

	for i := range m.Resources {
		r := &m.Resources[i]
		result := e.checkResource(ctx, m, r)
		md.Resources = append(md.Resources, result)
	}

	return md
}

func (e *Engine) checkResource(ctx context.Context, m *state.ModuleState, r *state.ModuleResource) DriftResult {
	res := DriftResult{
		Module:     m.Name,
		TypeName:   r.TypeName,
		Identifier: r.Identifier,
	}

	// CodeBuild projects are not managed via Cloud Control — use the
	// CodeBuildRunner auxiliary interface instead.
	if r.TypeName == "AWS::CodeBuild::Project" {
		return e.checkCodeBuildProject(ctx, res)
	}

	// All other resources use the standard ResourceClient.Get path.
	if e.ResourceGet == nil {
		res.Status = Error
		res.Details = "no resource client available"
		return res
	}

	cloudRes := &cloud.Resource{
		TypeName:   r.TypeName,
		Identifier: r.Identifier,
	}

	err := e.ResourceGet(ctx, cloudRes)
	if err != nil {
		if err == cloud.ErrResourceNotFound {
			res.Status = Missing
			res.Details = "resource not found in live state"
			return res
		}
		res.Status = Error
		res.Details = fmt.Sprintf("failed to query resource: %v", err)
		return res
	}

	// Resource exists — check for attribute mismatches based on type.
	return e.checkAttributes(res, m, r, cloudRes)
}

func (e *Engine) checkCodeBuildProject(ctx context.Context, res DriftResult) DriftResult {
	if e.CodeBuildRunner == nil {
		res.Status = Error
		res.Details = "no CodeBuildRunner available"
		return res
	}

	exists, err := e.CodeBuildRunner.ProjectExists(ctx, res.Identifier)
	if err != nil {
		res.Status = Error
		res.Details = fmt.Sprintf("failed to check project: %v", err)
		return res
	}
	if !exists {
		res.Status = Missing
		res.Details = "CodeBuild project not found"
		return res
	}
	res.Status = InSync
	return res
}

func (e *Engine) checkAttributes(res DriftResult, m *state.ModuleState, recorded *state.ModuleResource, live *cloud.Resource) DriftResult {
	switch recorded.TypeName {
	case cloud.TypeAWSEC2Instance:
		return e.checkEC2Instance(res, m, recorded, live)
	case cloud.TypeAWSEC2SecurityGroup:
		// Security group existence is sufficient for V1 drift.
		res.Status = InSync
		return res
	case cloud.TypeAWSIAMRole:
		// IAM role existence is sufficient for V1 drift.
		res.Status = InSync
		return res
	default:
		// For unknown types, existence is the only check we can do.
		res.Status = InSync
		res.Details = "existence-only check (attribute comparison unsupported for this type)"
		return res
	}
}

func (e *Engine) checkEC2Instance(res DriftResult, m *state.ModuleState, recorded *state.ModuleResource, live *cloud.Resource) DriftResult {
	// Parse live actual state for EC2 instance attributes.
	var actual struct {
		State struct {
			Name string `json:"Name"`
		} `json:"State"`
		InstanceType     string `json:"InstanceType"`
		ImageID          string `json:"ImageId"`
		PrivateIPAddress string `json:"PrivateIpAddress"`
	}

	if len(live.ActualState) == 0 {
		res.Status = InSync
		return res
	}

	if err := json.Unmarshal(live.ActualState, &actual); err != nil {
		res.Status = Error
		res.Details = fmt.Sprintf("failed to parse live state: %v", err)
		return res
	}

	// Check EC2 instance state — the expected state is "running".
	// "stopped" is drift for modules without a stop command (Horde, Perforce,
	// Lore, DDC). "terminated" means the instance was deleted outside Fabrica.
	if actual.State.Name != "" {
		switch actual.State.Name {
		case "running":
			// Expected state — no mismatch.
		case "stopped":
			res.Status = Mismatch
			res.Details = "instance stopped (expected running)"
			return res
		case "terminated":
			res.Status = Mismatch
			res.Details = "instance terminated (expected running)"
			return res
		default:
			// Transitional states (pending, stopping, shutting-down) are
			// not drift — the instance is still being managed.
		}
	}

	// Check recorded properties against live values.
	var mismatches []string
	if recorded.Properties != nil {
		if recType, ok := recorded.Properties["instanceType"]; ok && actual.InstanceType != "" {
			if recType != actual.InstanceType {
				mismatches = append(mismatches, fmt.Sprintf("InstanceType: recorded=%s, live=%s", recType, actual.InstanceType))
			}
		}
	}

	// AMI is stored in ModuleState.Version (not Properties) — compare it
	// against the live ImageId.
	if m.Version != "" && actual.ImageID != "" {
		if m.Version != actual.ImageID {
			mismatches = append(mismatches, fmt.Sprintf("ImageId: recorded=%s, live=%s", m.Version, actual.ImageID))
		}
	}

	if len(mismatches) > 0 {
		res.Status = Mismatch
		res.Details = "attribute mismatch: " + joinComma(mismatches)
		return res
	}

	res.Status = InSync
	return res
}

func joinComma(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return parts[0] + "; " + joinComma(parts[1:])
}
