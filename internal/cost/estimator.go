// Package cost provides a provider-agnostic cost estimation framework.
//
// Each cloud provider (AWS, GCP, Azure) registers estimators for its native
// resource type names against a single global registry. At estimate time,
// the registry looks up the appropriate Estimator by TypeName and runs it.
// This design lets Phase 1 modules register their own estimators and Phase 2+
// providers register for their own resource names against the same registry.
//
// Vigiles will later consume cost telemetry and diagnostics from this layer.
package cost

import (
	"fmt"
	"sync"
)

// Monthly represents an estimated monthly cost for a resource.
type Monthly struct {
	Amount     float64 // Estimated monthly cost in USD.
	Confidence ConfidenceLevel
	Note       string // Optional note explaining caveats (e.g. "based on minimal usage").
}

// ConfidenceLevel indicates how reliable the estimate is.
type ConfidenceLevel int

const (
	High ConfidenceLevel = iota // Well-constrained estimate (e.g. on-demand pricing).
	Medium                       // Reasonable estimate with some assumptions.
	Low                          // Very approximate — actual cost may differ significantly.
)

func (c ConfidenceLevel) String() string {
	switch c {
	case High:
		return "high"
	case Medium:
		return "medium"
	default:
		return "low"
	}
}

// Resource is the input shape for estimation. It matches cloud.Resource.
// Estimators receive a copy — they must not mutate it.
type Resource struct {
	TypeName string
}

// Estimator produces a monthly cost estimate for a single resource.
type Estimator interface {
	// Estimate returns the monthly cost for the given resource.
	Estimate(r Resource) (Monthly, error)
}

// Registry holds estimators keyed by resource TypeName.
type Registry struct {
	mu       sync.RWMutex
	estims   map[string]Estimator
}

// Global is the default cost registry.
var Global = &Registry{
	estims: make(map[string]Estimator),
}

// Register adds an estimator for the given resource TypeName.
// Panics on duplicate names — estimators call this from init(), so a conflict
// is a programming error that should be caught at startup.
func (r *Registry) Register(typeName string, e Estimator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.estims[typeName]; exists {
		panic(fmt.Sprintf("cost estimator %q already registered", typeName))
	}
	r.estims[typeName] = e
}

// Get returns the estimator registered for the given TypeName.
// Returns an error if no estimator is found, listing available types for debugging.
func (r *Registry) Get(typeName string) (Estimator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.estims[typeName]
	if !ok {
		avail := make([]string, 0, len(r.estims))
		for k := range r.estims {
			avail = append(avail, k)
		}
		return nil, fmt.Errorf("no cost estimator for %q; available: %v", typeName, avail)
	}
	return e, nil
}

// Estimate looks up and runs the estimator for the given TypeName.
func (r *Registry) Estimate(typeName string, resource Resource) (Monthly, error) {
	e, err := r.Get(typeName)
	if err != nil {
		return Monthly{}, err
	}
	return e.Estimate(resource)
}
