package destroy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const moduleName = "ddc"

// edgeInfo carries the region-scoped identifiers of one edge node.
type edgeInfo struct {
	region     string
	instanceID string
	sgID       string
}

// collectEdges groups the module's edge resources (role=edge) by region,
// sorted by region for deterministic teardown.
func collectEdges(m *fabricastate.ModuleState) []edgeInfo {
	byRegion := map[string]*edgeInfo{}
	var regionOrder []string
	for _, r := range m.Resources {
		if r.Properties == nil || r.Properties["role"] != ddc.RoleEdge {
			continue
		}
		region := r.Properties["region"]
		if region == "" {
			continue
		}
		e, ok := byRegion[region]
		if !ok {
			e = &edgeInfo{region: region}
			byRegion[region] = e
			regionOrder = append(regionOrder, region)
		}
		switch r.TypeName {
		case cloud.TypeAWSEC2Instance:
			e.instanceID = r.Identifier
		case cloud.TypeAWSEC2SecurityGroup:
			e.sgID = r.Identifier
		}
	}
	sort.Strings(regionOrder)
	edges := make([]edgeInfo, 0, len(regionOrder))
	for _, region := range regionOrder {
		edges = append(edges, *byRegion[region])
	}
	return edges
}

// isEdge returns whether a resource belongs to an edge region, and if so the
// region. Home resources return ("", false).
func isEdge(m *fabricastate.ModuleState, identifier string) (string, bool) {
	for _, r := range m.Resources {
		if r.Identifier == identifier && r.Properties != nil && r.Properties["role"] == ddc.RoleEdge {
			if region := r.Properties["region"]; region != "" {
				return region, true
			}
		}
	}
	return "", false
}

// resourceOrder: edge nodes first (instance then SG per region, regions
// sorted), then the home stack: coordinator → scylla → any unmarked EC2 →
// bucket → profile → role → home SG.
func resourceOrder(m *fabricastate.ModuleState) []cloud.Resource {
	var coord, scylla, otherEC2, bucket, profile, role, homeSG []cloud.Resource
	for _, r := range m.Resources {
		if r.Properties != nil && r.Properties["role"] == ddc.RoleEdge {
			continue
		}
		res := cloud.Resource{TypeName: r.TypeName, Identifier: r.Identifier}
		switch r.TypeName {
		case cloud.TypeAWSEC2Instance:
			roleName := ""
			if r.Properties != nil {
				roleName = r.Properties["role"]
			}
			switch roleName {
			case ddc.RoleCoordinator:
				coord = append(coord, res)
			case ddc.RoleScylla:
				scylla = append(scylla, res)
			default:
				otherEC2 = append(otherEC2, res)
			}
		case ddc.TypeAWSS3Bucket:
			bucket = append(bucket, res)
		case ddc.TypeAWSIAMInstanceProfile:
			profile = append(profile, res)
		case ddc.TypeAWSIAMRole:
			role = append(role, res)
		case cloud.TypeAWSEC2SecurityGroup:
			homeSG = append(homeSG, res)
		}
	}
	out := make([]cloud.Resource, 0, len(m.Resources))
	for _, e := range collectEdges(m) {
		if e.instanceID != "" {
			out = append(out, cloud.Resource{TypeName: cloud.TypeAWSEC2Instance, Identifier: e.instanceID})
		}
		if e.sgID != "" {
			out = append(out, cloud.Resource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: e.sgID})
		}
	}
	out = append(out, coord...)
	out = append(out, scylla...)
	out = append(out, otherEC2...)
	out = append(out, bucket...)
	out = append(out, profile...)
	out = append(out, role...)
	out = append(out, homeSG...)
	return out
}

var spec = teardown.Spec{
	ModuleName:     moduleName,
	Verb:           "destroy",
	VersionLabel:   "AMI ID",
	Title:          "Distributed DDC",
	NotProvisioned: "DDC is not provisioned. Nothing to destroy.",
	PlanHeader:     "Distributed DDC — destroy plan",
	DryRunHeader:   "Distributed DDC (destroy dry run)",
	Irreversible:   "IRREVERSIBLE: deletes edge nodes, the DDC instance, optional Scylla node, bucket (must be empty), IAM, and SG.",
	SuccessMessage: "Distributed DDC destroyed.",
	ResourceOrder:  resourceOrder,
}

// NewTeardown builds teardown for destroy --all.
func NewTeardown(rt globals.Runtime, out io.Writer) teardown.Command {
	tc := teardown.Command{
		Spec:        spec,
		Runtime:     rt,
		SkipConfirm: true,
		AssumeYes:   true,
		Out:         out,
		Confirm:     prompt.ConfirmExact,
		ReadState:   func() (*fabricastate.State, error) { return provision.ReadState(rt) },
		WriteState:  fabricastate.WriteState,
	}
	wire(&tc, rt)
	return tc
}

// wire sets the provider-backed seams shared by standalone and orchestrated
// teardowns, including the multi-region DeleteHook.
func wire(tc *teardown.Command, rt globals.Runtime) {
	if rt.Provider != nil {
		if rc := rt.Provider.Resources(); rc != nil {
			tc.DeleteResource = wrapDelete(rc.Delete)
			tc.GetResource = rc.Get
		}
		tc.DeleteHook = func(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, resources []cloud.Resource) ([]string, error) {
			return deleteWithRegions(ctx, *tc, st, m, resources, rt.Provider)
		}
	}
}

// deleteWithRegions deletes resources in order, routing each resource to its
// region-scoped client (edge nodes use their region's view, home uses the
// provider's default view).
func deleteWithRegions(ctx context.Context, tc teardown.Command, st *fabricastate.State, m *fabricastate.ModuleState, resources []cloud.Resource, p cloud.Provider) ([]string, error) {
	views := map[string]cloud.RegionView{}
	var destroyed []string
	for _, res := range resources {
		region, isEdge := isEdge(m, res.Identifier)
		dc := tc
		if isEdge {
			view, ok := views[region]
			if !ok {
				rp, ok := p.(cloud.RegionProvider)
				if !ok {
					return nil, fmt.Errorf("provider %q cannot delete edge resources in %s", p.Name(), region)
				}
				var err error
				view, err = rp.WithRegion(ctx, region)
				if err != nil {
					return nil, fmt.Errorf("binding region %s: %w", region, err)
				}
				views[region] = view
			}
			dc.DeleteResource = wrapDelete(view.Resources.Delete)
			dc.GetResource = view.Resources.Get
		}
		ids, err := dc.DeleteResources(ctx, st, m, []cloud.Resource{res})
		if err != nil {
			return nil, err
		}
		destroyed = append(destroyed, ids...)
	}
	return destroyed, nil
}

// wrapDelete adds a clearer message when S3 bucket delete fails (often non-empty).
func wrapDelete(del func(ctx context.Context, r *cloud.Resource) error) func(ctx context.Context, r *cloud.Resource) error {
	return func(ctx context.Context, r *cloud.Resource) error {
		err := del(ctx, r)
		if err == nil || errors.Is(err, cloud.ErrResourceNotFound) {
			return err
		}
		if r.TypeName == ddc.TypeAWSS3Bucket {
			return fmt.Errorf("deleting S3 bucket %s: %w\n"+
				"If the bucket is not empty, empty it first (or accept orphan cost), then re-run destroy.\n"+
				"Fabrica refuses to force-delete non-empty DDC blob buckets to protect cache data", r.Identifier, err)
		}
		return err
	}
}

// New returns the "ddc destroy" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Tear down DDC infrastructure (all regions)",
		Long: `Permanently delete the DDC stack: edge nodes (each in its region),
then the home stack (DDC EC2 → Scylla EC2 if any → S3 bucket → instance
profile → IAM role → home SG).

Non-empty S3 buckets are not force-deleted — empty the bucket and retry.
With --dry-run, shows the plan without AWS calls.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := teardown.Command{
				Spec:       spec,
				Runtime:    rt,
				DryRun:     opts.DryRun,
				AssumeYes:  opts.AssumeYes,
				JSONOut:    opts.JSONOutput,
				Out:        out,
				Confirm:    prompt.ConfirmExact,
				ReadState:  func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				WriteState: fabricastate.WriteState,
			}
			wire(&c, rt)
			return c.Run(cmd.Context())
		},
	}
}

// ResourceOrder exported for tests.
func ResourceOrder(m *fabricastate.ModuleState) []cloud.Resource {
	return resourceOrder(m)
}

// CollectEdges exported for tests.
func CollectEdges(m *fabricastate.ModuleState) []edgeInfo {
	return collectEdges(m)
}
