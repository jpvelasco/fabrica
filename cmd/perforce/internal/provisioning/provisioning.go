// Package provisioning resolves the state-backed resources required by
// Perforce operational commands.
package provisioning

import (
	"fmt"

	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
)

const (
	moduleName   = "perforce"
	instanceType = "AWS::EC2::Instance"
)

// Target is the provisioned Perforce module and its EC2 instance.
type Target struct {
	State    *fabricastate.State
	Module   *fabricastate.ModuleState
	Instance fabricastate.ModuleResource
}

// Resolve loads and validates the state required by Perforce operational
// commands. Command-specific status requirements remain with each caller.
func Resolve(readState func() (*fabricastate.State, error)) (Target, error) {
	st, err := readState()
	if err != nil {
		return Target{}, fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(moduleName)
	if m == nil {
		return Target{}, fmt.Errorf("Perforce is not provisioned. Run 'fabrica perforce create' first")
	}

	instance, ok := stateutil.ResourceByType(m, instanceType)
	if !ok || instance.Identifier == "" {
		return Target{}, fmt.Errorf("Perforce instance not found in state")
	}

	return Target{State: st, Module: m, Instance: instance}, nil
}
