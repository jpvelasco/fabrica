package terminate

import (
	"context"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/oplog"
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
	Irreversible:   "IRREVERSIBLE: This will permanently delete the workstation, IAM role/profile, the DCV session password parameter, and its data.",
	SuccessMessage: "Cloud workstation terminated.",
	// Parameter → instance → profile → role → SG. The SecureString goes first:
	// its name lives only on the instance resource, which is dropped from state
	// as soon as the instance is deleted — deleting the parameter before the
	// instance keeps the name referenceable until the delete succeeds, so a
	// failed or interrupted terminate cannot orphan it (issue #524).
	ResourceOrder: workstationResourceOrder,
	WireCommand:   wireSessionPasswordDelete,
}

// wireSessionPasswordDelete removes the DCV SecureString. The parameter is
// appended by workstationResourceOrder and is not a Cloud Control resource,
// so this hook handles only that type and returns nil. Every other type
// falls through. A missing parameter is success. Dry-run never calls it.
func wireSessionPasswordDelete(tc *teardown.Command, rt globals.Runtime) {
	if rt.Provider == nil {
		return
	}
	store, ok := rt.Provider.(cloud.ParameterStore)
	if !ok {
		return
	}
	tc.SDKDeleteFunc = func(ctx context.Context, typeName, identifier string) error {
		if typeName != cloud.TypeAWSSSMParameter {
			return cloud.ErrNotHandled
		}
		return store.DeleteParameter(ctx, identifier)
	}
}

func workstationResourceOrder(m *fabricastate.ModuleState) []cloud.Resource {
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
	// operator's copy stays in .fabrica/workstation-credentials.yaml.
	// Deleting a shared name could remove another instance's password, so
	// terminate deletes exactly the recorded name and skips when it is absent
	// (nothing recorded means no secret to clean up). Delete is success when
	// it is already gone.
	instRes := byType[cloud.TypeAWSEC2Instance]
	if name := instRes.Properties["sessionPasswordParameter"]; name != "" {
		// Validate before deleting: a malformed recorded name must not widen
		// the delete target to an arbitrary SSM parameter.
		if !workstation.IsSessionPasswordName(name) {
			oplog.WithModule("workstation").Warn("skipping DCV session password parameter: the recorded name is not a well-formed per-create name", "name", name)
		} else {
			out = append(out, cloud.Resource{TypeName: cloud.TypeAWSSSMParameter, Identifier: name})
		}
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
(/fabrica/workstation/<create-nonce>/dcv-password) is deleted too. Its name is
recorded in state on the instance, so terminate removes exactly the parameter
that create stored and skips when none is recorded. A parameter already gone
is success. .fabrica/workstation-credentials.yaml is left on disk.

State is updated after each deletion so a partial failure leaves a recoverable
record. Re-running terminate will skip resources that are already gone.

Before deleting the instance, the current EC2 state is checked:
  - stopping / shutting-down: terminate exits with an error; retry once complete.
  - terminated / not found: treated as already deleted; state is cleaned up.

With --dry-run, shows the terminate plan without making any AWS calls.`,
	}, spec, runtimeSource, optionsSource, out)
}
