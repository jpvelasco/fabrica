package horde

import (
	"encoding/json"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
)

// ConnectionProperty is shared by coordinator and dependent resource records.
const ConnectionProperty = "hordeConnection"

// IsCoordinator excludes agent resources from coordinator state resolution.
func IsCoordinator(r state.ModuleResource) bool {
	return r.Properties["role"] != "agent" && (r.TypeName == cloud.TypeAWSEC2Instance || r.TypeName == cloud.TypeAWSEC2SecurityGroup)
}

// CoordinatorResource selects a coordinator resource even when agent records
// precede it. Role filtering lives with the connection contract.
func CoordinatorResource(m *state.ModuleState, typeName string) (state.ModuleResource, bool) {
	if m != nil {
		for _, r := range m.Resources {
			if r.TypeName == typeName && r.Properties["role"] != "agent" {
				return r, true
			}
		}
	}
	return state.ModuleResource{}, false
}

// RecordedConnection validates all present coordinator records, including a
// partial create that has persisted only its security group.
func RecordedConnection(m *state.ModuleState) (*Connection, error) {
	if m == nil {
		return nil, nil
	}
	var recorded *Connection
	for _, r := range m.Resources {
		if !IsCoordinator(r) {
			continue
		}
		raw, present := r.Properties[ConnectionProperty]
		if !present {
			continue
		}
		var c Connection
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, fmt.Errorf("invalid recorded Horde connection; restore state: %w", err)
		}
		if err := c.validate(); err != nil {
			return nil, err
		}
		if recorded != nil && *recorded != c {
			return nil, fmt.Errorf("coordinator Horde connection records disagree; restore consistent state before continuing")
		}
		recorded = &c
	}
	return recorded, nil
}

// ResolveConnection rejects unverified migrations and configuration downgrades
// before callers mutate resources or read a service token.
func ResolveConnection(cfg config.HordeConfig, m *state.ModuleState) (Connection, error) {
	desired, err := ConnectionFromConfig(cfg)
	if err != nil {
		return Connection{}, err
	}
	recorded, err := RecordedConnection(m)
	if err != nil {
		return Connection{}, err
	}
	if m == nil || (recorded == nil && !desired.TLS) {
		return desired, nil
	}
	if recorded == nil || (!recorded.TLS && desired.TLS) {
		return Connection{}, fmt.Errorf("Horde requires TLS migration verification; install the runtime and run 'fabrica horde tls adopt'")
	}
	if *recorded != desired {
		return Connection{}, fmt.Errorf("Horde configuration differs from its recorded connection; restore the recorded horde TLS identity, paths, and ports")
	}
	return *recorded, nil
}

// ValidateDependent prevents stamping a desired transport onto an existing
// agent pool or project that has not actually installed that contract.
func ValidateDependent(c Connection, r state.ModuleResource, contract string) error {
	if !c.TLS {
		return nil
	}
	if r.Properties[contract] != "1" || r.Properties[ConnectionProperty] != c.Record() {
		return fmt.Errorf("existing Horde dependent lacks the verified TLS contract; drain and replace the agent pool or CI project after 'fabrica horde tls adopt'")
	}
	return nil
}
