package destroy

import (
	"context"
	"errors"
	"fmt"
	"io"

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

// resourceOrder: DDC instance(s) first (coordinator then scylla by role), then
// bucket, instance profile, role, SG. Delete by identifier list.
func resourceOrder(m *fabricastate.ModuleState) []cloud.Resource {
	var coord, scylla, otherEC2 []cloud.Resource
	var bucket, profile, role, sg []cloud.Resource
	for _, r := range m.Resources {
		res := cloud.Resource{TypeName: r.TypeName, Identifier: r.Identifier}
		switch r.TypeName {
		case ddc.TypeAWSEC2Instance:
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
		case ddc.TypeAWSEC2SecurityGroup:
			sg = append(sg, res)
		}
	}
	// Coordinator first, then scylla, then any unmarked EC2.
	out := append(coord, scylla...)
	out = append(out, otherEC2...)
	out = append(out, bucket...)
	out = append(out, profile...)
	out = append(out, role...)
	out = append(out, sg...)
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
	Irreversible:   "IRREVERSIBLE: deletes the DDC instance, optional Scylla node, bucket (must be empty), IAM, and SG.",
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
	if rt.Provider != nil {
		if rc := rt.Provider.Resources(); rc != nil {
			tc.DeleteResource = wrapDelete(rc.Delete)
			tc.GetResource = rc.Get
		}
	}
	return tc
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
				"V1 refuses to force-delete non-empty DDC blob buckets to protect cache data", r.Identifier, err)
		}
		return err
	}
}

// New returns the "ddc destroy" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Tear down DDC infrastructure",
		Long: `Permanently delete the home-region DDC stack.

Deletion order: DDC EC2 → Scylla EC2 (if any) → S3 bucket → instance profile → IAM role → SG.
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
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					c.DeleteResource = wrapDelete(rc.Delete)
					c.GetResource = rc.Get
				}
			}
			return c.Run(cmd.Context())
		},
	}
}

// ResourceOrder exported for tests.
func ResourceOrder(m *fabricastate.ModuleState) []cloud.Resource {
	return resourceOrder(m)
}
