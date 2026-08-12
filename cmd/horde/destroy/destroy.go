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
	ModuleName:     "horde",
	Verb:           "destroy",
	VersionLabel:   "AMI ID",
	Title:          "Unreal Horde build coordinator",
	NotProvisioned: "Horde is not provisioned. Nothing to destroy.",
	PlanHeader:     "Unreal Horde build coordinator — destroy plan",
	DryRunHeader:   "Unreal Horde build coordinator (destroy dry run)",
	Irreversible:   "IRREVERSIBLE: This will permanently delete the Horde coordinator, IAM role/profile, and its data.",
	SuccessMessage: "Unreal Horde build coordinator destroyed.",
	// Instance → profile → role → SG (reverse of create: SG → role → profile → instance).
	ResourceOrder: hordeResourceOrder,
}

func hordeResourceOrder(m *fabricastate.ModuleState) []cloud.Resource {
	// Full destroy order: agents first (ASG → LT → agent IAM → agent SG),
	// then coordinator (instance → coordinator IAM → coordinator SG).
	// Agent resources are identified by Properties["role"] = "agent".

	isAgentResource := func(r fabricastate.ModuleResource) bool {
		return r.Properties != nil && r.Properties["role"] == "agent"
	}

	// Group resources by deletion phase.
	type phase struct {
		name    string
		matchFn func(fabricastate.ModuleResource) bool
	}

	phases := []phase{
		{"scaling-policy", func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::AutoScaling::ScalingPolicy" }},
		{"alarm", func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::CloudWatch::Alarm" }},
		{"asg", func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::AutoScaling::AutoScalingGroup" }},
		{"lt", func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::EC2::LaunchTemplate" }},
		{"instance", func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::EC2::Instance" }},
		{"agent-profile", func(r fabricastate.ModuleResource) bool {
			return r.TypeName == "AWS::IAM::InstanceProfile" && isAgentResource(r)
		}},
		{"coord-profile", func(r fabricastate.ModuleResource) bool {
			return r.TypeName == "AWS::IAM::InstanceProfile" && !isAgentResource(r)
		}},
		{"agent-role", func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::IAM::Role" && isAgentResource(r) }},
		{"coord-role", func(r fabricastate.ModuleResource) bool { return r.TypeName == "AWS::IAM::Role" && !isAgentResource(r) }},
		{"agent-sg", func(r fabricastate.ModuleResource) bool {
			return r.TypeName == "AWS::EC2::SecurityGroup" && isAgentResource(r)
		}},
		{"coord-sg", func(r fabricastate.ModuleResource) bool {
			return r.TypeName == "AWS::EC2::SecurityGroup" && !isAgentResource(r)
		}},
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

// New returns the "horde destroy" subcommand. Global flags (--dry-run, --yes,
// --json) are resolved at execution time via the source closures.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return teardown.NewStandaloneCommand(&cobra.Command{
		Use:   "destroy",
		Short: "Permanently delete the Unreal Horde build coordinator",
		Long: `Permanently delete the Unreal Horde build coordinator and all its AWS resources.

Resources are deleted in reverse-creation order to respect dependencies:
  1. EC2 Instance (terminated first)
  2. EC2 Security Group

State is updated after each deletion so a partial failure leaves a recoverable
record. Re-running destroy will skip resources that are already gone.

Before deleting the instance, the current EC2 state is checked:
  - stopping / shutting-down: destroy exits with an error; retry once complete.
  - terminated / not found: treated as already deleted; state is cleaned up.

With --dry-run, shows the destroy plan without making any AWS calls.`,
	}, spec, runtimeSource, optionsSource, out)
}
