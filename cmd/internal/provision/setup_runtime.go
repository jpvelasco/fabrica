package provision

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/state"
)

// PrepareSetupRuntime resolves the target into a config copy before backend
// reads or locking. It performs identity I/O only, without saving configuration.
func PrepareSetupRuntime(ctx context.Context, rt globals.Runtime) (globals.Runtime, state.SetupPlan, error) {
	if rt.Config == nil {
		return globals.Runtime{}, state.SetupPlan{}, fmt.Errorf("no setup configuration loaded; load the intended config before setup")
	}
	if _, err := rt.Config.ResolveIdentity(); err != nil {
		return globals.Runtime{}, state.SetupPlan{}, err
	}
	if rt.Provider == nil {
		return globals.Runtime{}, state.SetupPlan{}, fmt.Errorf("no setup provider loaded; load the intended provider before setup")
	}
	account, _, region, err := rt.Provider.Identity(ctx)
	if err != nil {
		return globals.Runtime{}, state.SetupPlan{}, fmt.Errorf("resolving setup identity: %w", err)
	}
	if account == "" || region == "" {
		return globals.Runtime{}, state.SetupPlan{}, fmt.Errorf("setup identity has no account or region; verify provider credentials and region before setup")
	}
	target := &state.State{Account: account, Region: region}
	if err := state.ValidateIdentity(target, rt.Config.Cloud.AWS.AccountID, rt.Config.Cloud.AWS.Region); err != nil {
		return globals.Runtime{}, state.SetupPlan{}, fmt.Errorf("setup target mismatch (provider account %q and region %q vs configured account %q and region %q); restore the intended configuration and inspect the authenticated target", account, region, rt.Config.Cloud.AWS.AccountID, rt.Config.Cloud.AWS.Region)
	}
	rt.Config = rt.Config.Clone()
	rt.Config.Cloud.AWS.AccountID, rt.Config.Cloud.AWS.Region = account, region
	plan := state.NewSetupPlan(rt.Config, account, region)
	rt.Config.State.Bucket, rt.Config.State.Table = plan.Backend.Bucket, plan.Backend.Table
	return rt, plan, nil
}
