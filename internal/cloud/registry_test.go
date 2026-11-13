package cloud

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestGetRejectsCustomOwnershipBeforeConstructor(t *testing.T) {
	name := t.Name()
	called := false
	Register(name, func(*config.Config) (Provider, error) {
		called = true
		return nil, nil
	})
	t.Cleanup(func() {
		reg.mu.Lock()
		defer reg.mu.Unlock()
		delete(reg.providers, name)
	})
	cfg := config.Defaults()
	cfg.Cloud.AWS.Identity.ManagedBy = "Studio-Tools"
	if _, err := Get(name, cfg); err == nil || !strings.Contains(err.Error(), "custom ownership is not enabled") || called {
		t.Fatalf("custom ownership reached constructor: called=%t, error=%v", called, err)
	}
	cfg.Cloud.AWS.Identity.ManagedBy = ""
	if _, err := Get(name, cfg); err != nil || !called {
		t.Fatalf("default constructor blocked: called=%t, error=%v", called, err)
	}
}
