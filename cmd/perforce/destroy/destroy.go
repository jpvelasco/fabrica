package destroy

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/perforce"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

var spec = teardown.Spec{
	ModuleName:     "perforce",
	Verb:           "destroy",
	VersionLabel:   "Version",
	Title:          "Perforce Helix Core",
	NotProvisioned: "Perforce is not provisioned. Nothing to destroy.",
	PlanHeader:     "Perforce Helix Core — destroy plan",
	DryRunHeader:   "Perforce Helix Core (destroy dry run)",
	Irreversible: "IRREVERSIBLE: This deletes the Perforce instance, security group, IAM profile/role, and the admin password SecureString. " +
		"The data volume is retained (DeleteOnTermination=false) so local backups survive as an orphan EBS volume. " +
		"S3 exports are not deleted. Delete backups explicitly if you intend a full purge.",
	SuccessMessage: "Perforce Helix Core destroyed. Data volume (and any local backups on it) retained; S3 exports untouched.",
	// Admin-password SecureString → instance → profile → role → SG. The
	// parameter is the first in the order; see wirePerforce.
	WireCommand: wirePerforce,
}

// wirePerforce attaches the SDK parameter delete and installs the resource
// order, which needs the configured name prefix to validate the recorded
// admin-password name (#705).
func wirePerforce(tc *teardown.Command, rt globals.Runtime) {
	teardown.WireParameterDelete(tc, rt)
	tc.Spec.ResourceOrder = func(m *fabricastate.ModuleState) []cloud.Resource {
		// Load rejects non-default prefixes until #705 is enabled, so this
		// error cannot occur on a loaded config.
		prefix, _ := rt.Config.ResolveNamePrefix()
		return perforceResourceOrder(m, prefix)
	}
}

// perforceResourceOrder deletes the recorded admin-password SecureString first.
// Its name lives only on the instance record, which the teardown engine drops
// from state as soon as the instance is deleted, so a parameter delete that ran
// after the instance could orphan the secret on a failed or interrupted run
// (the #524 pattern). A pre-#700 instance records no name and is skipped.
func perforceResourceOrder(m *fabricastate.ModuleState, prefix naming.Prefix) []cloud.Resource {
	order := []string{
		"AWS::EC2::Instance",
		"AWS::IAM::InstanceProfile",
		"AWS::IAM::Role",
		"AWS::EC2::SecurityGroup",
	}
	byType := map[string]fabricastate.ModuleResource{}
	for _, r := range m.Resources {
		byType[r.TypeName] = r
	}
	out := make([]cloud.Resource, 0, len(order)+1)
	inst := byType[cloud.TypeAWSEC2Instance]
	if r, ok := teardown.RecordedParameter("perforce", inst.Properties["adminPasswordParameter"], perforce.AdminPasswordScope(prefix).IsName); ok {
		out = append(out, r)
	}
	for _, t := range order {
		if r, ok := byType[t]; ok && r.Identifier != "" {
			out = append(out, cloud.Resource{TypeName: r.TypeName, Identifier: r.Identifier})
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

// New returns the "perforce destroy" subcommand. Global flags (--dry-run,
// --yes, --json) are resolved at execution time via the source closures.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return teardown.NewStandaloneCommand(&cobra.Command{
		Use:   "destroy",
		Short: "Permanently delete the Perforce Helix Core server",
		Long: `Permanently delete the Perforce Helix Core server and all its AWS resources.

Resources are deleted in dependency order:
  1. SSM Parameter Store SecureString (the Helix Core admin password, first so a
     failed or interrupted run cannot orphan it)
  2. EC2 Instance (terminated second)
  3. IAM Instance Profile
  4. IAM Role
  5. EC2 Security Group

The admin-password SecureString (/<namePrefix>/perforce/<create-nonce>/admin-password,
/fabrica/perforce/... by default) is deleted by name, as recorded on the instance.
An instance created before the password moved to SSM records no name, so there is
nothing to delete. A parameter already gone is success.

The data volume is retained (DeleteOnTermination=false). S3 exports are not deleted.

State is updated after each deletion so a partial failure leaves a recoverable
record. Re-running destroy will skip resources that are already gone.

Before deleting the instance, the current EC2 state is checked:
  - stopping / shutting-down: destroy exits with an error; retry once complete.
  - terminated / not found: treated as already deleted; state is cleaned up.

With --dry-run, shows the destroy plan without making any AWS calls.`,
	}, spec, runtimeSource, optionsSource, out)
}
