package provision

import (
	"context"
	"fmt"
	"io"

	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// CreateSpec describes the common lifecycle around a module-specific create
// plan. Plan construction, rendering, and resource creation remain owned by the
// calling command.
type CreateSpec[P any] struct {
	ModuleName      string
	Account         string
	Plan            P
	DryRun          bool
	AssumeYes       bool
	Out             io.Writer
	ExistingMessage string
	Confirm         func(string, string) bool
	ReadState       func() (*fabricastate.State, error)
	PrintDryRun     func(P)
	PrintApplyPlan  func(P)
	Apply           func(context.Context, *fabricastate.State, P) error
}

// RunCreate enforces the shared create lifecycle: dry runs stop before state
// access, existing modules stop before confirmation, and resource creation only
// begins after confirmation succeeds.
func RunCreate[P any](ctx context.Context, spec CreateSpec[P]) error {
	if spec.DryRun {
		spec.PrintDryRun(spec.Plan)
		return nil
	}

	st, err := spec.ReadState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	if st.GetModule(spec.ModuleName) != nil {
		fmt.Fprint(spec.Out, spec.ExistingMessage)
		return nil
	}

	spec.PrintApplyPlan(spec.Plan)
	if !ConfirmCreate(spec.Out, spec.ModuleName, spec.Account, spec.AssumeYes, spec.Confirm) {
		return nil
	}

	return spec.Apply(ctx, st, spec.Plan)
}
