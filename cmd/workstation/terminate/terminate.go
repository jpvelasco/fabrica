package terminate

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/naming"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/workstation"
	"github.com/spf13/cobra"
)

var spec = teardown.Spec{
	ModuleName:     "workstation",
	Verb:           "terminate",
	VersionLabel:   "AMI ID",
	Title:          "Cloud Workstation",
	NotProvisioned: "Workstation is not provisioned. Nothing to terminate.",
	PlanHeader:     "Cloud Workstation — terminate plan",
	DryRunHeader:   "Cloud Workstation (terminate dry run)",
	Irreversible:   "IRREVERSIBLE: This will permanently delete the workstation, IAM role/profile, the DCV session password parameter, its data, and the local " + workstation.CredentialsFile + " record.",
	SuccessMessage: "Cloud workstation terminated.",
	// After a fully successful teardown, the local DCV session-password
	// record is stale: the instance it describes no longer exists. A file
	// that cannot be removed is reported as a warning (issue #511).
	LocalCleanupFile: workstation.CredentialsFile,
	// Parameter → instance → profile → role → SG. The SecureString goes first:
	// its name lives only on the instance resource, which is dropped from state
	// as soon as the instance is deleted — deleting the parameter before the
	// instance keeps the name referenceable until the delete succeeds, so a
	// failed or interrupted terminate cannot orphan it (issue #524).
	WireCommand: wireWorkstation,
}

// wireWorkstation attaches the SDK parameter delete and installs the resource
// order, which needs the configured name prefix to validate the recorded
// session-password name (#705).
func wireWorkstation(tc *teardown.Command, rt globals.Runtime) {
	teardown.WireParameterDelete(tc, rt)
	tc.Spec.ResourceOrder = func(m *fabricastate.ModuleState) []cloud.Resource {
		// Load rejects non-default prefixes until #705 is enabled, so this
		// error cannot occur on a loaded config.
		prefix, _ := rt.Config.ResolveNamePrefix()
		return workstationResourceOrder(m, prefix)
	}
}

func workstationResourceOrder(m *fabricastate.ModuleState, prefix naming.Prefix) []cloud.Resource {
	byType := map[string]fabricastate.ModuleResource{}
	for _, r := range m.Resources {
		byType[r.TypeName] = r
	}
	out := make([]cloud.Resource, 0, 5)
	appendRecorded := func(typeName string) {
		if r, ok := byType[typeName]; ok && r.Identifier != "" {
			out = append(out, cloud.Resource{TypeName: r.TypeName, Identifier: r.Identifier})
		}
	}
	// The DCV session-password SecureString is deleted before the EC2
	// instance. Its per-create name is recorded in state on the instance
	// (Properties "sessionPasswordParameter"), not as a state resource — a
	// drift Get of AWS::SSM::Parameter would return the secret. The instance
	// record is dropped from state as soon as the instance is deleted, so a
	// parameter delete that runs after the instance would lose the only name
	// reference and orphan the SecureString on a failed or interrupted run.
	// Deleting it first is safe: cloud-init reads it at boot only, and the
	// operator's copy stays in .fabrica/workstation-credentials.yaml until
	// the teardown fully completes (the engine removes it last).
	// Deleting a shared name could remove another instance's password, so
	// terminate deletes exactly the recorded name and skips when it is absent
	// (nothing recorded means no secret to clean up). Delete is success when
	// it is already gone.
	// The recorded name is validated before it becomes a delete target, so a
	// malformed name cannot widen the delete to an arbitrary SSM parameter.
	instRes := byType[cloud.TypeAWSEC2Instance]
	if r, ok := teardown.RecordedParameter("workstation", instRes.Properties["sessionPasswordParameter"], workstation.SessionPasswordScope(prefix).IsName); ok {
		out = append(out, r)
	}
	appendRecorded(cloud.TypeAWSEC2Instance)
	appendRecorded(cloud.TypeAWSIAMInstanceProfile)
	appendRecorded(cloud.TypeAWSIAMRole)
	appendRecorded(cloud.TypeAWSEC2SecurityGroup)
	return out
}

// NewTeardown builds this module's teardown.Command for orchestrated use by
// `fabrica destroy --all`. Confirmation is skipped (the orchestrator confirms
// the aggregate operation).
func NewTeardown(rt globals.Runtime, out io.Writer) teardown.Command {
	return teardown.NewTeardown(spec, rt, out)
}

// New returns the "workstation terminate" subcommand. Global flags (--dry-run,
// --yes, --json) are resolved at execution time via the source closures.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return teardown.NewStandaloneCommand(&cobra.Command{
		Use:   "terminate",
		Short: "Permanently terminate the cloud workstation",
		Long: `Permanently terminate the cloud workstation and all its AWS resources.

Resources are deleted in dependency order:
  1. SSM Parameter Store SecureString (the DCV session password, first so a
     failed or interrupted run cannot orphan it)
  2. EC2 Instance (terminated second)
  3. IAM Instance Profile
  4. IAM Role
  5. EC2 Security Group

The per-create DCV session password SecureString
(/<namePrefix>/workstation/<create-nonce>/dcv-password, /fabrica/workstation/...
by default) is deleted too. Its name is
recorded in state on the instance, so terminate removes exactly the parameter
that create stored and skips when none is recorded. A parameter already gone
is success. After a fully successful teardown, terminate also removes the
local .fabrica/workstation-credentials.yaml record; a file it cannot remove is
reported as a warning so the operator can delete it by hand.

State is updated after each deletion so a partial failure leaves a recoverable
record. Re-running terminate will skip resources that are already gone.

Before deleting the instance, the current EC2 state is checked:
  - stopping / shutting-down: terminate exits with an error; retry once complete.
  - terminated / not found: treated as already deleted; state is cleaned up.

With --dry-run, shows the terminate plan without making any AWS calls.`,
	}, spec, runtimeSource, optionsSource, out)
}
