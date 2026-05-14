package cloud

import (
	"fmt"
	"sort"
	"sync"

	"github.com/jpvelasco/fabrica/internal/config"
)

// Constructor builds a Provider from a Fabrica config.
type Constructor func(cfg *config.Config) (Provider, error)

type registry struct {
	mu       sync.RWMutex
	providers map[string]Constructor
}

var reg = &registry{
	providers: make(map[string]Constructor),
}

// Register registers a provider constructor under the given name.
// Panics on duplicate names — providers call this from init(), so a conflict
// is a programming error that should be caught at startup.
func Register(name string, fn Constructor) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if _, exists := reg.providers[name]; exists {
		panic(fmt.Sprintf("provider %q already registered", name))
	}
	reg.providers[name] = fn
}

// Get returns the provider registered under name, constructed with the given config.
func Get(name string, cfg *config.Config) (Provider, error) {
	reg.mu.RLock()
	fn, ok := reg.providers[name]
	reg.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("provider %q not registered; available: %v", name, Names())
	}
	return fn(cfg)
}

// Names returns the sorted list of all registered provider names.
func Names() []string {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	names := make([]string, 0, len(reg.providers))
	for n := range reg.providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
