package destroy

import (
	"context"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

var spec = teardown.Spec{
	ModuleName:     "lore",
	Verb:           "destroy",
	VersionLabel:   "AMI ID",
	Title:          "Lore loreserver",
	NotProvisioned: "Lore is not provisioned. Nothing to destroy.",
	PlanHeader:     "Lore loreserver — destroy plan",
	DryRunHeader:   "Lore loreserver (destroy dry run)",
	Irreversible:   "IRREVERSIBLE: This will permanently delete the Lore server and its data.",
	SuccessMessage: "Lore loreserver destroyed.",
	ResourceOrder:  loreResourceOrder,
	WireCommand:    wireSDKDelete,
}

// wireSDKDelete attaches the S3 purge hook so the versioned store bucket is
// emptied (all versions + delete markers) before Cloud Control deletes it.
// Without purging, any data ever written blocks bucket deletion forever.
func wireSDKDelete(tc *teardown.Command, rt globals.Runtime) {
	if rt.Provider == nil {
		return
	}
	cleaner, ok := rt.Provider.(cloud.S3BucketCleaner)
	if !ok {
		return
	}
	tc.SDKDeleteFunc = func(ctx context.Context, typeName, identifier string) error {
		if typeName != cloud.TypeAWSS3Bucket {
			return cloud.ErrNotHandled
		}
		if err := cleaner.PurgeBucket(ctx, identifier); err != nil {
			return fmt.Errorf("emptying S3 bucket %s: %w", identifier, err)
		}
		// Purged — hand the now-empty bucket back to Cloud Control deletion.
		return cloud.ErrNotHandled
	}
}

// loreResourceOrder returns the deletion sequence for the Lore module.
// Standard: Instance → IAM profile → IAM role → DynamoDB tables → S3 bucket → SG.
// When S3 store backend is disabled: Instance → SG (profiles/role/tables/bucket absent).
// Tables are deleted after the instance and role (the store is provably
// quiescent) and before the bucket (the bucket purge doesn't touch DynamoDB,
// but the tables must not outlive the store they describe).
//
// Honesty: this function honors every AWS::DynamoDB::Table row already present
// in state — it does not guess table names from the bucket string. State is
// the source of truth; a previous bug dropped tables from the plan when the
// table list was derived only from config.storeBackend / bucket, leaving four
// orphans after destroy. Table-only leftovers (instance/role/bucket already
// gone) are still returned so a re-run can clean them.
func loreResourceOrder(m *fabricastate.ModuleState) []cloud.Resource {
	type phase struct {
		matchFn func(fabricastate.ModuleResource) bool
	}

	phases := []phase{
		{matchFn: func(r fabricastate.ModuleResource) bool { return r.TypeName == cloud.TypeAWSEC2Instance }},
		{matchFn: func(r fabricastate.ModuleResource) bool { return r.TypeName == cloud.TypeAWSIAMInstanceProfile }},
		{matchFn: func(r fabricastate.ModuleResource) bool { return r.TypeName == cloud.TypeAWSIAMRole }},
		{matchFn: func(r fabricastate.ModuleResource) bool { return r.TypeName == cloud.TypeAWSDynamoDBTable }},
		{matchFn: func(r fabricastate.ModuleResource) bool { return r.TypeName == cloud.TypeAWSS3Bucket }},
		{matchFn: func(r fabricastate.ModuleResource) bool { return r.TypeName == cloud.TypeAWSEC2SecurityGroup }},
	}

	out := make([]cloud.Resource, 0, len(m.Resources))
	for _, p := range phases {
		for _, r := range m.Resources {
			if p.matchFn(r) && r.Identifier != "" {
				out = append(out, cloud.Resource{TypeName: r.TypeName, Identifier: r.Identifier})
			}
		}
	}
	return out
}

// NewTeardown builds this module's teardown.Command for orchestrated use by
// `fabrica destroy --all`. Confirmation is skipped (the orchestrator confirms
// the aggregate operation).
func NewTeardown(rt globals.Runtime, out io.Writer) teardown.Command {
	return teardown.NewTeardown(spec, rt, out)
}

// New returns the "lore destroy" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return teardown.NewStandaloneCommand(&cobra.Command{
		Use:   "destroy",
		Short: "Permanently delete the Lore server",
		Long: `Permanently delete the Lore loreserver and all its AWS resources.

Resources are deleted in reverse-creation order to respect dependencies:
  1. EC2 Instance (terminated first)
  2. IAM Instance Profile (if S3 store enabled)
  3. IAM Role (if S3 store enabled)
  4. DynamoDB store tables (if S3 store enabled)
  5. S3 Store Bucket (if S3 store enabled)
  6. EC2 Security Group

State is updated after each deletion so a partial failure leaves a recoverable
record. Re-running destroy will skip resources that are already gone.

With --dry-run, shows the destroy plan without making any AWS calls.`,
	}, spec, runtimeSource, optionsSource, out)
}
