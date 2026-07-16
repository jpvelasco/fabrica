// Package stateutil provides shared helpers for querying module state.
package stateutil

import fabricastate "github.com/jpvelasco/fabrica/internal/state"

// ResourceByType returns the first resource with the given TypeName from m.
// Returns the zero value and false if no matching resource exists.
func ResourceByType(m *fabricastate.ModuleState, typeName string) (fabricastate.ModuleResource, bool) {
	for _, r := range m.Resources {
		if r.TypeName == typeName {
			return r, true
		}
	}
	return fabricastate.ModuleResource{}, false
}
