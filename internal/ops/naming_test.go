package ops

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

func TestHooksFollowPrefix(t *testing.T) {
	acme := naming.MustResolve("acme")
	cfg := config.OpsConfig{Enabled: true, Modules: []string{"ci"}}
	doc, err := BuildExport(acme, cfg)
	if err != nil {
		t.Fatalf("BuildExport: %v", err)
	}
	if len(doc.Hooks) != 1 {
		t.Fatalf("hooks = %+v, want one ci hook", doc.Hooks)
	}
	if h := doc.Hooks[0]; h.LogGroup != "/acme/ci" || h.Alarm != "acme-ci-unhealthy" {
		t.Errorf("hook = %+v, want /acme/ci and acme-ci-unhealthy", h)
	}
	res := CostResources(acme, cfg)
	if len(res) != 2 || res[0].Name != "acme-ci-unhealthy" || res[1].Name != "/acme/ci" {
		t.Errorf("cost resources = %+v, want acme-ci-unhealthy and /acme/ci", res)
	}
}
