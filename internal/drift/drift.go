// Package drift provides drift detection and optional auto-remediation for
// Fabrica-managed resources. It compares recorded state (.fabrica/state.json)
// against live AWS resources and reports whether each resource is in sync,
// missing, extra, or has attribute mismatches.
//
// The engine is provider-agnostic: it accepts a state snapshot and provider
// interfaces (ResourceClient, StateBackendChecker, CodeBuildRunner) and produces
// a DriftReport. No AWS SDK imports belong here.
//
// Extra detection uses ResourceClient.List to enumerate live resources per
// type and diff against recorded state identifiers. CodeBuild projects are
// excluded from Extra checks since CodeBuildRunner has no List method.
//
// Remediation (V1) supports recreating Missing EC2 instances and security
// groups from recorded state. Mismatch and Extra are report-only in V1.
package drift

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/credentials"
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/lore"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/workstation"
)

// DriftStatus represents the drift state of a single resource.
type DriftStatus string

const (
	InSync   DriftStatus = "inSync"
	Missing  DriftStatus = "missing"
	Extra    DriftStatus = "extra"
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
	Extra    int           `json:"extra"`
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
	State        *state.State
	ResourceGet  func(ctx context.Context, r *cloud.Resource) error
	ResourceList func(ctx context.Context, typeName string) ([]cloud.Resource, error)
	// RegionResource resolves a region-scoped resource client for resources
	// recorded in a non-home region (DDC edges). Nil when the provider does
	// not support additional regions; such resources are reported as Error.
	RegionResource  func(ctx context.Context, region string) (cloud.ResourceClient, error)
	BackendChecker  cloud.StateBackendChecker
	CodeBuildRunner cloud.CodeBuildRunner
	Config          *DriftConfig

	// regionClients caches region-scoped clients resolved during one Run.
	regionClients map[string]cloud.ResourceClient
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
	e.regionClients = map[string]cloud.ResourceClient{}

	// Check state backend health.
	report.Backend = e.checkBackend(ctx)

	// Check each module's resources.
	for i := range e.State.Modules {
		md := e.checkModule(ctx, &e.State.Modules[i])
		report.Modules = append(report.Modules, md)
	}

	// Check for extra resources (live but not in any module's state).
	// This is done at the report level so we can diff against all recorded
	// identifiers across all modules, not just one module at a time.
	extras := e.checkExtraResources(ctx)
	for i := range report.Modules {
		md := &report.Modules[i]
		for j := range extras {
			ex := &extras[j]
			if ex.Module == md.Name {
				md.Resources = append(md.Resources, *ex)
			}
		}
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
			case Extra:
				report.Extra++
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

// checkExtraResources lists live resources for each type across all modules
// and reports any that are not recorded in state. CodeBuild projects are
// excluded since CodeBuildRunner has no List method.
//
// Note: ResourceClient.List returns all resources of a type in the account,
// not scoped to a module. We diff against all recorded identifiers across all
// modules to avoid false positives when multiple modules share the same
// resource type (e.g., IAM roles). Extras are assigned to the first module
// that records the matching type name.
//
// Home-region resources are listed through the home client; resources
// recorded in an edge region (DDC edges) are additionally listed through
// that region's client so edge resources are not reported as missing by the
// home list and strays created in an edge region surface as Extra.
func (e *Engine) checkExtraResources(ctx context.Context) []DriftResult {
	if e.ResourceList == nil {
		return nil
	}

	// Build sets of ALL recorded identifiers across ALL modules.
	allRecorded := make(map[string]map[string]bool)
	// Track which module owns each type (first module that has the type).
	typeOwner := make(map[string]string)
	// Track which resource types are recorded per region ("" = home).
	regionTypes := make(map[string]map[string]bool)
	for i := range e.State.Modules {
		sm := &e.State.Modules[i]
		for j := range sm.Resources {
			r := &sm.Resources[j]
			if r.TypeName == "AWS::CodeBuild::Project" {
				continue
			}
			if allRecorded[r.TypeName] == nil {
				allRecorded[r.TypeName] = make(map[string]bool)
				typeOwner[r.TypeName] = sm.Name
			}
			allRecorded[r.TypeName][r.Identifier] = true
			region := resourceRegion(r)
			if regionTypes[region] == nil {
				regionTypes[region] = make(map[string]bool)
			}
			regionTypes[region][r.TypeName] = true
		}
	}

	var results []DriftResult
	for typeName, recordedIDs := range allRecorded {
		live, err := e.ResourceList(ctx, typeName)
		if err != nil {
			continue
		}
		results = append(results, e.extraResults(ctx, typeName, live, recordedIDs, typeOwner[typeName])...)
	}

	// Edge regions: enumerate the types recorded there through that
	// region's client. Regions that cannot be resolved (no region seam)
	// are skipped — the home list already covers the home side.
	for region, types := range regionTypes {
		if region == "" || region == e.homeRegion() {
			continue
		}
		rc, err := e.regionClient(ctx, region)
		if err != nil {
			continue
		}
		for typeName := range types {
			live, err := rc.List(ctx, typeName)
			if err != nil {
				continue
			}
			results = append(results, e.extraResults(ctx, typeName, live, allRecorded[typeName], typeOwner[typeName])...)
		}
	}

	return results
}

// extraResults diffs one type's live list against the recorded identifiers
// and returns the unrecorded (Extra) ones. ASG-managed instances carry
// FabricaRole=agent in their tags and are not tracked individually in
// state, so they are excluded.
func (e *Engine) extraResults(_ context.Context, typeName string, live []cloud.Resource, recordedIDs map[string]bool, owner string) []DriftResult {
	var results []DriftResult
	for _, res := range live {
		if recordedIDs[res.Identifier] {
			continue
		}
		if isASGManagedInstance(res) {
			continue
		}
		results = append(results, DriftResult{
			Module:     owner,
			TypeName:   typeName,
			Identifier: res.Identifier,
			Status:     Extra,
			Details:    "resource exists in live state but not in recorded state",
		})
	}
	return results
}

// isASGManagedInstance returns true if the resource is an EC2 instance with
// FabricaRole=agent in its tags. These are ASG-launched instances that are
// not tracked individually in Fabrica state and should be excluded from
// Extra detection.
func isASGManagedInstance(res cloud.Resource) bool {
	if res.TypeName != "AWS::EC2::Instance" {
		return false
	}
	if len(res.ActualState) == 0 {
		return false
	}
	var props struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	if err := json.Unmarshal(res.ActualState, &props); err != nil {
		return false
	}
	for _, tag := range props.Tags {
		if tag.Key == "FabricaRole" && tag.Value == "agent" {
			return true
		}
	}
	return false
}

// homeRegion returns the region the home client is bound to.
func (e *Engine) homeRegion() string {
	if e.Config == nil {
		return ""
	}
	return e.Config.Region
}

// resourceRegion returns the region a recorded resource belongs to; the
// empty string means the home region.
func resourceRegion(r *state.ModuleResource) string {
	if r.Properties == nil {
		return ""
	}
	return r.Properties["region"]
}

// regionClient resolves (and caches) the resource client for a non-home
// region via the RegionResource seam.
func (e *Engine) regionClient(ctx context.Context, region string) (cloud.ResourceClient, error) {
	if e.RegionResource == nil {
		return nil, fmt.Errorf("provider does not support additional regions")
	}
	if c, ok := e.regionClients[region]; ok {
		return c, nil
	}
	c, err := e.RegionResource(ctx, region)
	if err != nil {
		return nil, err
	}
	e.regionClients[region] = c
	return c, nil
}

// getFor routes a Get to the client bound to the resource's region: the
// home seam for home-region resources, the region-scoped client otherwise.
func (e *Engine) getFor(ctx context.Context, r *state.ModuleResource, cloudRes *cloud.Resource) error {
	region := resourceRegion(r)
	if region == "" || region == e.homeRegion() {
		return e.ResourceGet(ctx, cloudRes)
	}
	rc, err := e.regionClient(ctx, region)
	if err != nil {
		return fmt.Errorf("routing to region %s: %w", region, err)
	}
	return rc.Get(ctx, cloudRes)
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

	err := e.getFor(ctx, r, cloudRes)
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
	case cloud.TypeAWSS3Bucket:
		// S3 bucket existence is sufficient for V1 drift.
		res.Status = InSync
		return res
	case cloud.TypeAWSIAMInstanceProfile:
		// IAM instance profile existence is sufficient for V1 drift.
		res.Status = InSync
		return res
	case cloud.TypeAWSDynamoDBTable:
		// DynamoDB table existence is sufficient for V1 drift (Lore S3
		// store tables).
		res.Status = InSync
		return res
	case cloud.TypeAWSAutoScalingScalingPolicy:
		// Scaling policy existence is sufficient for V1 drift.
		res.Status = InSync
		return res
	case cloud.TypeAWSCloudWatchAlarm:
		// CloudWatch alarm existence is sufficient for V1 drift.
		res.Status = InSync
		return res
	case cloud.TypeAWSAutoScalingAutoScalingGroup:
		// ASG existence is sufficient for V1 drift (capacity is dynamic).
		res.Status = InSync
		return res
	case cloud.TypeAWSEC2LaunchTemplate:
		// Launch template existence is sufficient for V1 drift.
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
	// Lore, DDC). Workstation ships supported stop/start lifecycle commands,
	// so a deliberately parked workstation is in sync. "terminated" means the
	// instance was deleted outside Fabrica.
	if actual.State.Name == "stopped" && m.Name != "workstation" {
		res.Status = Mismatch
		res.Details = "instance stopped (expected running)"
		return res
	}
	if actual.State.Name == "terminated" {
		res.Status = Mismatch
		res.Details = "instance terminated (expected running)"
		return res
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

	// AMI is stored in Properties["imageId"] when available (Perforce V0.4+),
	// otherwise falls back to ModuleState.Version (Horde, Lore, DDC).
	recordedAMI := ""
	if recorded.Properties != nil {
		recordedAMI = recorded.Properties["imageId"]
	}
	if recordedAMI == "" && m.Version != "" {
		recordedAMI = m.Version
	}
	if recordedAMI != "" && actual.ImageID != "" {
		if recordedAMI != actual.ImageID {
			mismatches = append(mismatches, fmt.Sprintf("ImageId: recorded=%s, live=%s", recordedAMI, actual.ImageID))
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

// ActionKind describes what the remediation engine will do for a drifted
// resource.
type ActionKind string

const (
	// ActionCreate means the resource is Missing and will be recreated from
	// recorded state.
	ActionCreate ActionKind = "create"
	// ActionSkip means the drift kind is not supported for auto-fix (Mismatch,
	// Extra, or Error) and will be reported only.
	ActionSkip ActionKind = "skip"
)

// RemediationAction describes one remediation step for a drifted resource.
type RemediationAction struct {
	Module     string `json:"module"`
	TypeName   string `json:"typeName"`
	Identifier string `json:"identifier"`
	// Region is the resource's region when it is recorded in a non-home
	// region (DDC edges); empty for home-region resources. Remediation
	// must create the resource through that region's client.
	Region string      `json:"region,omitempty"`
	Drift  DriftStatus `json:"drift"`
	Kind   ActionKind  `json:"kind"`
	Reason string      `json:"reason,omitempty"`
	// Resource is the cloud.Resource to create (populated for ActionCreate).
	Resource *cloud.Resource `json:"-"`
}

// RemediationPlan is the output of PlanRemediation: a list of actions to take
// and a summary.
type RemediationPlan struct {
	Actions    []RemediationAction `json:"actions"`
	ToFix      int                 `json:"toFix"`
	ToSkip     int                 `json:"toSkip"`
	ReportOnly int                 `json:"reportOnly"`
}

// RemediationResult is the output of ApplyRemediation: what was applied,
// skipped, or failed.
type RemediationResult struct {
	Applied []RemediationAction `json:"applied"`
	Skipped []RemediationAction `json:"skipped"`
	Failed  []RemediationAction `json:"failed"`
	Errors  []string            `json:"errors,omitempty"`
}

// PlanRemediation takes a DriftReport and the state + config, and returns a
// RemediationPlan describing what would be fixed. Missing EC2 instances and
// security groups are marked for creation. Mismatch and Extra are report-only
// in V1.
func PlanRemediation(report *DriftReport, st *state.State, cfg *config.Config) *RemediationPlan {
	plan := &RemediationPlan{}

	for i := range report.Modules {
		md := &report.Modules[i]
		for j := range md.Resources {
			r := &md.Resources[j]
			region := recordedRegion(st, r.Module, r.TypeName, r.Identifier)

			var action RemediationAction
			switch r.Status {
			case Missing:
				if canRecreate(r.TypeName) {
					res, err := rebuildResource(r, st, cfg)
					if err != nil {
						action = RemediationAction{
							Module:     r.Module,
							TypeName:   r.TypeName,
							Identifier: r.Identifier,
							Drift:      r.Status,
							Kind:       ActionSkip,
							Reason:     fmt.Sprintf("cannot rebuild %s from recorded state: %v; recreate manually via fabrica", r.TypeName, err),
							Region:     region,
						}
						plan.ToSkip++
						plan.ReportOnly++
					} else {
						action = RemediationAction{
							Module:     r.Module,
							TypeName:   r.TypeName,
							Identifier: r.Identifier,
							Drift:      r.Status,
							Kind:       ActionCreate,
							Resource:   res,
							Region:     region,
						}
						plan.ToFix++
					}
				} else {
					action = RemediationAction{
						Module:     r.Module,
						TypeName:   r.TypeName,
						Identifier: r.Identifier,
						Drift:      r.Status,
						Kind:       ActionSkip,
						Reason:     fmt.Sprintf("auto-remediation unsupported for %s; recreate manually via fabrica", r.TypeName),
						Region:     region,
					}
					plan.ToSkip++
					plan.ReportOnly++
				}
			case Extra:
				action = RemediationAction{
					Module:     r.Module,
					TypeName:   r.TypeName,
					Identifier: r.Identifier,
					Drift:      r.Status,
					Kind:       ActionSkip,
					Reason:     "extra resources are report-only in V1; manually delete if needed",
					Region:     region,
				}
				plan.ToSkip++
				plan.ReportOnly++
			case Mismatch:
				action = RemediationAction{
					Module:     r.Module,
					TypeName:   r.TypeName,
					Identifier: r.Identifier,
					Drift:      r.Status,
					Kind:       ActionSkip,
					Reason:     "mismatch remediation is report-only in V1; " + r.Details,
					Region:     region,
				}
				plan.ToSkip++
				plan.ReportOnly++
			case Error:
				action = RemediationAction{
					Module:     r.Module,
					TypeName:   r.TypeName,
					Identifier: r.Identifier,
					Drift:      r.Status,
					Kind:       ActionSkip,
					Reason:     "cannot remediate: " + r.Details,
					Region:     region,
				}
				plan.ToSkip++
			case InSync:
				// Nothing to do — the resource is in sync.
				continue
			}

			plan.Actions = append(plan.Actions, action)
		}
	}

	return plan
}

// canRecreate returns true if the resource type can be recreated from recorded
// state via Cloud Control. Only EC2 instances and security groups are supported
// in V1. IAM roles, CodeBuild projects, and other types require module-specific
// setup and are not auto-remediated.
func canRecreate(typeName string) bool {
	switch typeName {
	case cloud.TypeAWSEC2Instance, cloud.TypeAWSEC2SecurityGroup:
		return true
	default:
		return false
	}
}

// recordedRegion returns the region recorded on the resource in state; the
// empty string means the home region.
func recordedRegion(st *state.State, module, typeName, identifier string) string {
	mod := st.GetModule(module)
	if mod == nil {
		return ""
	}
	for i := range mod.Resources {
		r := &mod.Resources[i]
		if r.TypeName == typeName && r.Identifier == identifier {
			return resourceRegion(r)
		}
	}
	return ""
}

// rebuildResource reconstructs a cloud.Resource from the drift result, state,
// and module config so remediation can recreate it. Returns an error when the
// recorded state is insufficient to rebuild the resource's desired state;
// the caller then reports the resource as skip-with-reason instead of
// creating it with a partial state.
// and config so it can be recreated via the provider. It uses the module's
// recorded properties and config defaults to build the desired state.
func rebuildResource(dr *DriftResult, st *state.State, cfg *config.Config) (*cloud.Resource, error) {
	mod := st.GetModule(dr.Module)
	if mod == nil {
		return &cloud.Resource{
			TypeName:   dr.TypeName,
			Identifier: dr.Identifier,
		}, nil
	}

	// Find the recorded resource in state.
	var rec *state.ModuleResource
	for i := range mod.Resources {
		if mod.Resources[i].TypeName == dr.TypeName && mod.Resources[i].Identifier == dr.Identifier {
			rec = &mod.Resources[i]
			break
		}
	}

	res := &cloud.Resource{
		TypeName:   dr.TypeName,
		Identifier: dr.Identifier,
	}

	if rec == nil {
		return res, nil
	}

	// Build desired state from recorded properties and config defaults.
	switch dr.TypeName {
	case cloud.TypeAWSEC2Instance:
		ds, err := rebuildInstanceDesiredState(rec, mod, cfg)
		if err != nil {
			return nil, fmt.Errorf("rebuild desired state for instance %s in module %s: %w", dr.Identifier, dr.Module, err)
		}
		res.DesiredState = ds
	case cloud.TypeAWSEC2SecurityGroup:
		ds, err := rebuildSGDesiredState(rec, mod, cfg)
		if err != nil {
			return nil, fmt.Errorf("rebuild desired state for security group %s in module %s: %w", dr.Identifier, dr.Module, err)
		}
		res.DesiredState = ds
	}

	return res, nil
}

// rebuildInstanceDesiredState builds the desired state of a Missing EC2
// instance. Perforce is reconstructed through its full create contract
// (rebuildPerforceInstance): recorded sizing, security group, IAM instance
// profile, and cloud-init. The other modules get a minimal launch contract
// from the module version (the AMI) and the instanceType from properties
// (falling back to module config). DDC edge instances are region-specific
// and are rebuilt from their recorded inputs only
// (rebuildEdgeInstanceDesiredState).
func rebuildInstanceDesiredState(rec *state.ModuleResource, mod *state.ModuleState, cfg *config.Config) (json.RawMessage, error) {
	if mod.Name == "ddc" && isEdgeResource(rec) {
		return rebuildEdgeInstanceDesiredState(rec, mod, cfg)
	}

	if mod.Name == "perforce" {
		return rebuildPerforceInstance(rec, mod, cfg)
	}

	instanceType := ""
	ami := mod.Version
	subnetID := ""

	// Resolve per-module config based on module name.
	switch mod.Name {
	case "horde":
		instanceType = cfg.Horde.InstanceType
		if ami == "" {
			ami = cfg.Horde.AmiID
		}
		subnetID = cfg.Horde.SubnetId
	case "lore":
		instanceType = cfg.Lore.InstanceType
		if ami == "" {
			ami = cfg.Lore.AmiID
		}
		subnetID = cfg.Lore.SubnetId
	case "ddc":
		instanceType = cfg.DDC.InstanceType
		if ami == "" {
			ami = cfg.DDC.AmiID
		}
		subnetID = cfg.DDC.SubnetId
	case "workstation":
		instanceType = cfg.Workstation.InstanceType
		if ami == "" {
			ami = cfg.Workstation.AmiID
		}
		subnetID = cfg.Workstation.SubnetId
	}

	// Recorded properties override config defaults.
	if v, ok := rec.Properties["instanceType"]; ok && v != "" {
		instanceType = v
	}
	if v, ok := rec.Properties["subnetId"]; ok && v != "" {
		subnetID = v
	}

	ds := map[string]any{
		"InstanceType": instanceType,
		"ImageId":      ami,
	}

	if subnetID != "" {
		ds["SubnetId"] = subnetID
	}

	// Security group is linked by finding the SG resource in the same module;
	// DDC edge instances pair with the edge group recorded in their own region.
	// Note: if the SG is also Missing, its identifier in state may be updated
	// by ApplyRemediation after the SG is recreated. The instance's DesiredState
	// is baked at plan time, so ApplyRemediation must refresh it after SG create.
	sgID := moduleSGIDFor(rec, mod)
	if sgID != "" {
		ds["SecurityGroupIds"] = []string{sgID}
	}

	return json.Marshal(ds)
}

// instanceTypeFor resolves the launch instance type for one module: the
// recorded property wins, then the module config, then the module default the
// create command uses.
func instanceTypeFor(rec *state.ModuleResource, cfgType, moduleDefault string) string {
	if v := rec.Properties["instanceType"]; v != "" {
		return v
	}
	if cfgType != "" {
		return cfgType
	}
	return moduleDefault
}

// volumeSizeFor resolves the data volume size for one module: the recorded
// property wins, then the module config, then the module default the create
// command uses.
func volumeSizeFor(rec *state.ModuleResource, cfgSize, moduleDefault int) int {
	if v, ok := rec.Properties["volumeSize"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if cfgSize > 0 {
		return cfgSize
	}
	return moduleDefault
}

// subnetFor resolves the launch subnet for one module: the recorded property
// wins, then the module config. Empty means the account default VPC.
func subnetFor(rec *state.ModuleResource, cfgSubnet string) string {
	if v := rec.Properties["subnetId"]; v != "" {
		return v
	}
	return cfgSubnet
}

// recordedSGAndProfile returns the security group and IAM instance profile
// identifiers a rebuilt instance must carry: the module records one of each
// at create, and an instance launched without either cannot register with
// SSM or join its VPC.
func recordedSGAndProfile(rec *state.ModuleResource, mod *state.ModuleState, module string) (string, string, error) {
	sgID := moduleSGIDFor(rec, mod)
	if sgID == "" {
		return "", "", fmt.Errorf("no security group recorded for the %s module; re-run the %s create command", module, module)
	}
	profile := moduleInstanceProfileFor(mod)
	if profile == "" {
		return "", "", fmt.Errorf("no IAM instance profile recorded for the %s module; re-run the %s create command", module, module)
	}
	return sgID, profile, nil
}

// Credential file paths the create commands write, relative to the working
// directory (the .fabrica/ directory that holds the state cache).
const (
	perforceCredFile = ".fabrica/perforce-credentials.yaml"
)

// readPerforcePassword is a seam over the create-time credentials file.
// Tests swap it with a fake; the real reader comes from internal/credentials.
var readPerforcePassword = credentials.ReadPerforceAdminPassword

// rebuildPerforceInstance rebuilds the Perforce instance. The AMI is the
// recorded Properties.imageId: Perforce's module version is the Helix Core
// software version, so it is used as the pinned version in cloud-init
// instead. The admin password comes from the create-time credentials file.
func rebuildPerforceInstance(rec *state.ModuleResource, mod *state.ModuleState, cfg *config.Config) (json.RawMessage, error) {
	ami := rec.Properties["imageId"]
	if ami == "" {
		return nil, fmt.Errorf("no AMI recorded for the perforce instance (Properties.imageId); re-run 'fabrica perforce create'")
	}
	helixVersion := mod.Version
	if helixVersion == "" {
		helixVersion = cfg.Perforce.Version
	}
	if helixVersion == "" {
		helixVersion = perforce.DefaultHelixVersion
	}
	// Structural inputs are checked before the credential file read: a
	// state that cannot supply the full launch contract should say so
	// without touching .fabrica.
	sgID, profile, err := recordedSGAndProfile(rec, mod, "perforce")
	if err != nil {
		return nil, err
	}
	adminPass, err := readPerforcePassword(perforceCredFile)
	if err != nil {
		return nil, err
	}
	userData, err := perforce.Generate(perforce.UserDataConfig{
		Version:   helixVersion,
		ServerID:  "fabrica-perforce",
		AdminPass: adminPass,
	})
	if err != nil {
		return nil, fmt.Errorf("rendering perforce cloud-init: %w", err)
	}
	plan := &perforce.CreatePlan{
		InstanceType: instanceTypeFor(rec, cfg.Perforce.InstanceType, perforce.DefaultInstanceType),
		VolumeSize:   volumeSizeFor(rec, cfg.Perforce.VolumeSize, 500),
		SubnetID:     subnetFor(rec, cfg.Perforce.SubnetId),
		InstanceName: "fabrica-perforce",
	}
	ds, err := perforce.InstanceDesiredState(plan, sgID, userData, profile, ami)
	if err != nil {
		return nil, err
	}
	return stripEmptySubnetID(ds)
}

// isEdgeResource reports whether a recorded resource is a DDC edge resource:
// it carries the edge role plus an explicit region. Home resources record
// neither, so they never match.
func isEdgeResource(rec *state.ModuleResource) bool {
	if rec.Properties == nil {
		return false
	}
	return rec.Properties["role"] == ddc.RoleEdge && rec.Properties["region"] != ""
}

// rebuildEdgeInstanceDesiredState rebuilds a Missing DDC edge instance's
// desired state from its recorded region-specific inputs. Edge AMIs and
// subnets are per-region, so the recorded imageId and subnetId are required;
// resources recorded before those inputs were captured are reported
// unrebuildable with a hint to re-run the edge provisioning command.
func rebuildEdgeInstanceDesiredState(rec *state.ModuleResource, mod *state.ModuleState, cfg *config.Config) (json.RawMessage, error) {
	region := rec.Properties["region"]
	ami := rec.Properties["imageId"]
	if ami == "" {
		return nil, fmt.Errorf("no AMI recorded for DDC edge %s; re-run 'fabrica ddc region add %s'", region, region)
	}
	subnetID := rec.Properties["subnetId"]
	if subnetID == "" {
		return nil, fmt.Errorf("no subnet recorded for DDC edge %s; re-run 'fabrica ddc region add %s'", region, region)
	}

	instanceType := rec.Properties["instanceType"]
	if instanceType == "" {
		instanceType = cfg.DDC.InstanceType
	}
	if instanceType == "" {
		instanceType = ddc.DefaultInstanceType
	}

	ds := map[string]any{
		"InstanceType": instanceType,
		"ImageId":      ami,
		"SubnetId":     subnetID,
	}
	if sgID := moduleSGIDFor(rec, mod); sgID != "" {
		ds["SecurityGroupIds"] = []string{sgID}
	}
	return json.Marshal(ds)
}

// moduleSGIDFor returns the security group identifier an instance's desired
// state should reference. DDC edge instances pair with the edge group
// recorded in their own region (the home group sits in another VPC). Home
// instances pair with the group whose recorded role matches their own (both
// absent is the module's default group), so a coordinator instance is never
// paired with an agent-pool group even when the agent group is recorded
// first; the fallback is the first group in the module.
func moduleSGIDFor(rec *state.ModuleResource, mod *state.ModuleState) string {
	if isEdgeResource(rec) {
		for i := range mod.Resources {
			r := &mod.Resources[i]
			if r.TypeName == cloud.TypeAWSEC2SecurityGroup && isEdgeResource(r) && r.Properties["region"] == rec.Properties["region"] {
				return r.Identifier
			}
		}
		return ""
	}
	role := ""
	if rec.Properties != nil {
		role = rec.Properties["role"]
	}
	var fallback string
	for i := range mod.Resources {
		r := &mod.Resources[i]
		if r.TypeName != cloud.TypeAWSEC2SecurityGroup {
			continue
		}
		if r.Properties["role"] == role {
			return r.Identifier
		}
		if fallback == "" {
			fallback = r.Identifier
		}
	}
	return fallback
}

// moduleInstanceProfileFor returns the identifier of the IAM instance profile
// the module recorded in state, or "" when none is recorded. The first
// profile wins; modules record exactly one.
func moduleInstanceProfileFor(mod *state.ModuleState) string {
	for i := range mod.Resources {
		r := &mod.Resources[i]
		if r.TypeName == cloud.TypeAWSIAMInstanceProfile {
			return r.Identifier
		}
	}
	return ""
}

// rebuildSGDesiredState rebuilds a Missing security group's desired state by
// reusing each module's canonical SG builder against the resolved config, so
// the remediated group matches what the create command would have
// provisioned: configured ports, distinct public/internal CIDRs, and
// role-specific groups (horde agent pool, DDC edge nodes). Recorded state
// properties (vpcId, role, region) override config where they exist.
func rebuildSGDesiredState(rec *state.ModuleResource, mod *state.ModuleState, cfg *config.Config) (json.RawMessage, error) {
	ds, err := moduleSGDispatch(rec, mod, cfg)
	if err != nil {
		return nil, err
	}
	return stripEmptyVPCID(ds)
}

// moduleSGDispatch is the per-module SG dispatch. It is a var seam so tests
// can force a builder failure through rebuildSGDesiredState's error path —
// the canonical builders only fail on JSON marshaling, which is unreachable
// in production.
var moduleSGDispatch = dispatchModuleSG

// dispatchModuleSG builds the canonical SG desired state for one module's
// recorded resource against the resolved configuration.
func dispatchModuleSG(rec *state.ModuleResource, mod *state.ModuleState, cfg *config.Config) (json.RawMessage, error) {
	var ds json.RawMessage
	var err error

	switch mod.Name {
	case "horde":
		if rec.Properties["role"] == "agent" {
			// Agent-pool group: agents dial the coordinator, so no ingress.
			ds, err = horde.AgentSGDesiredState(&horde.AgentsCreatePlan{
				SGName: horde.DefaultAgentsSGName,
				VPCID:  sgVPCID(rec, agentsVPC(cfg)),
			})
			break
		}
		port := cfg.Horde.Port
		if port <= 0 {
			port = horde.DefaultPort
		}
		grpcPort := cfg.Horde.GRPCPort
		if grpcPort <= 0 {
			grpcPort = horde.DefaultGRPCPort
		}
		ds, err = horde.SGDesiredState(&horde.CreatePlan{
			SGName:      horde.DefaultSGName,
			VPCID:       sgVPCID(rec, cfg.Horde.VPCId),
			Port:        port,
			GRPCPort:    grpcPort,
			AllowedCIDR: defaultCIDR(cfg.Horde.AllowedCIDR, horde.DefaultAllowedCIDR),
		})
	case "perforce":
		ds, err = perforce.SGDesiredState(&perforce.CreatePlan{
			SGName:      perforce.DefaultSGName,
			VPCID:       sgVPCID(rec, cfg.Perforce.VPCId),
			AllowedCIDR: defaultCIDR(cfg.Perforce.AllowedCIDR, perforce.DefaultAllowedCIDR),
		})
	case "lore":
		ds, err = lore.SGDesiredState(&lore.CreatePlan{
			SGName:      lore.DefaultSGName,
			VPCID:       sgVPCID(rec, cfg.Lore.VPCId),
			GRPCPort:    lore.DefaultGRPCPort,
			HTTPPort:    lore.DefaultHTTPPort,
			AllowedCIDR: defaultCIDR(cfg.Lore.AllowedCIDR, lore.DefaultAllowedCIDR),
		})
	case "ddc":
		ds, err = rebuildDDCSG(rec, cfg)
	case "workstation":
		ds, err = workstation.SGDesiredState(&workstation.CreatePlan{
			SGName:      workstation.DefaultSGName,
			VPCID:       sgVPCID(rec, cfg.Workstation.VPCId),
			DCVPort:     workstation.DefaultDCVPort,
			AllowedCIDR: defaultCIDR(cfg.Workstation.AllowedCIDR, workstation.DefaultAllowedCIDR),
		})
	case "ci":
		ds, err = ci.SGDesiredState(&ci.CreatePlan{
			SGName: ci.DefaultSGName,
			VPCID:  sgVPCID(rec, cfg.CI.VPCId),
		})
	default:
		ds, err = minimalSGDesiredState(rec)
	}
	return ds, err
}

// rebuildDDCSG rebuilds a DDC security group. Edge nodes (recorded
// Properties.role = ddc.RoleEdge with a region) get the edge shape named per
// region; everything else gets the home shape, including the Scylla CQL rule
// when the configured backend is scylla.
func rebuildDDCSG(rec *state.ModuleResource, cfg *config.Config) (json.RawMessage, error) {
	publicPort := cfg.DDC.PublicPort
	if publicPort <= 0 {
		publicPort = ddc.DefaultPublicPort
	}
	internalPort := cfg.DDC.InternalPort
	if internalPort <= 0 {
		internalPort = ddc.DefaultInternalPort
	}
	plan := ddc.SetupPlan{
		SGName:       ddc.DefaultSGName,
		VPCID:        sgVPCID(rec, cfg.DDC.VPCId),
		PublicPort:   publicPort,
		InternalPort: internalPort,
		AllowedCIDR:  defaultCIDR(cfg.DDC.AllowedCIDR, ddc.DefaultAllowedCIDR),
		InternalCIDR: defaultCIDR(cfg.DDC.InternalCIDR, ddc.DefaultAllowedCIDR),
		Backend:      ddc.NormalizeBackend(cfg.DDC.Backend),
	}
	if isEdgeResource(rec) {
		region := rec.Properties["region"]
		// Edge groups live in the edge region's VPC, so the recorded VPC is
		// required; the home-config VPC fallback would point at the wrong one.
		vpcID := rec.Properties["vpcId"]
		if vpcID == "" {
			return nil, fmt.Errorf("no VPC recorded for DDC edge %s; re-run 'fabrica ddc region add %s'", region, region)
		}
		return ddc.EdgeSGDesiredState(&ddc.EdgePlan{
			SGName:       ddc.EdgeSGName(region),
			VPCID:        vpcID,
			PublicPort:   publicPort,
			InternalPort: internalPort,
			AllowedCIDR:  plan.AllowedCIDR,
			InternalCIDR: plan.InternalCIDR,
		})
	}
	return ddc.SGDesiredState(&plan)
}

// sgVPCID returns the VPC recorded on the SG's state properties, falling
// back to the module-config VPC when nothing is recorded.
func sgVPCID(rec *state.ModuleResource, cfgVPC string) string {
	if v, ok := rec.Properties["vpcId"]; ok && v != "" {
		return v
	}
	return cfgVPC
}

// agentsVPC mirrors the agent placement resolution: an explicit
// horde.agents.vpcId wins, otherwise the pool follows the coordinator's VPC.
func agentsVPC(cfg *config.Config) string {
	if cfg.Horde.Agents.VPCID != "" {
		return cfg.Horde.Agents.VPCID
	}
	return cfg.Horde.VPCId
}

// defaultCIDR returns the configured CIDR, falling back to the module default
// the create command uses when the config field is empty.
func defaultCIDR(cfgCIDR, fallback string) string {
	if cfgCIDR != "" {
		return cfgCIDR
	}
	return fallback
}

// minimalSGDesiredState is the fallback for modules without a canonical SG
// builder: a description plus the recorded VPC when known.
func minimalSGDesiredState(rec *state.ModuleResource) (json.RawMessage, error) {
	ds := map[string]any{
		"GroupDescription": "Fabrica-managed security group",
	}
	if v, ok := rec.Properties["vpcId"]; ok && v != "" {
		ds["VpcId"] = v
	}
	return json.Marshal(ds)
}

// stripEmptyVPCID drops the VpcId key when no VPC could be resolved. An
// empty-string VpcId would be an invalid Cloud Control property; omitting it
// targets the account default VPC, matching the previous minimal rebuild.
func stripEmptyVPCID(ds json.RawMessage) (json.RawMessage, error) {
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		return nil, err
	}
	if m["VpcId"] == "" {
		delete(m, "VpcId")
	}
	return json.Marshal(m)
}

// stripEmptySubnetID drops the SubnetId key when no subnet could be resolved.
// The canonical instance builders emit the key unconditionally; an empty
// string is an invalid Cloud Control property, while omitting it targets the
// account default VPC.
func stripEmptySubnetID(ds json.RawMessage) (json.RawMessage, error) {
	var m map[string]any
	if err := json.Unmarshal(ds, &m); err != nil {
		return nil, err
	}
	if m["SubnetId"] == "" {
		delete(m, "SubnetId")
	}
	return json.Marshal(m)
}
