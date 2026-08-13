package destroy

import (
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
}

// loreResourceOrder returns the deletion sequence for the Lore module.
// Standard: Instance → IAM profile → IAM role → S3 bucket → SG.
// When S3 store backend is disabled: Instance → SG (profiles/role/bucket absent).
func loreResourceOrder(m *fabricastate.ModuleState) []cloud.Resource {
	type phase struct {
		matchFn func(fabricastate.ModuleResource) bool
	}

	phases := []phase{
		{matchFn: func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::EC2::Instance" }},
		{matchFn: func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::IAM::InstanceProfile" }},
		{matchFn: func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::IAM::Role" }},
		{matchFn: func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::S3::Bucket" }},
		{matchFn: func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::EC2::SecurityGroup" }},
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
  4. S3 Store Bucket (if S3 store enabled)
  5. EC2 Security Group

State is updated after each deletion so a partial failure leaves a recoverable
record. Re-running destroy will skip resources that are already gone.

With --dry-run, shows the destroy plan without making any AWS calls.`,
	}, spec, runtimeSource, optionsSource, out)
}
