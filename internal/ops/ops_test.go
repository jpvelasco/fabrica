package ops

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

func TestResolveModulesDisabled(t *testing.T) {
	got, err := ResolveModules(config.OpsConfig{})
	if err != nil {
		t.Fatalf("disabled: %v", err)
	}
	if got != nil {
		t.Fatalf("disabled modules = %v, want nil", got)
	}
}

func TestResolveModulesDefaultAndUnknown(t *testing.T) {
	got, err := ResolveModules(config.OpsConfig{Enabled: true})
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if len(got) != len(SupportedModules) {
		t.Fatalf("default = %v, want %v", got, SupportedModules)
	}
	if _, err := ResolveModules(config.OpsConfig{Enabled: true, Modules: []string{"mystery"}}); err == nil {
		t.Fatal("expected unknown module error")
	}
}

func TestBuildExportAndCost(t *testing.T) {
	cfg := config.OpsConfig{Enabled: true, Modules: []string{"horde", "perforce"}, LogRetentionDays: 14}
	exp, err := BuildExport(cfg)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exp.LogRetentionDays != 14 || len(exp.Hooks) != 2 {
		t.Fatalf("export = %+v", exp)
	}
	if exp.Hooks[0].LogGroup == "" || exp.Hooks[0].Alarm == "" {
		t.Fatalf("hook incomplete: %+v", exp.Hooks[0])
	}
	res := CostResources(cfg)
	if len(res) != 4 {
		t.Fatalf("cost resources = %d, want 4: %+v", len(res), res)
	}
	if CostResources(config.OpsConfig{}) != nil {
		t.Fatal("disabled ops should have no cost")
	}
	var sawLog, sawAlarm bool
	for _, r := range res {
		switch r.TypeName {
		case TypeAWSLogsLogGroup:
			sawLog = true
			if cost.Global.EstimateAll([]cost.Resource{r}).Total <= 0 {
				t.Fatal("expected log-group estimate")
			}
		case cloud.TypeAWSCloudWatchAlarm:
			sawAlarm = true
		}
	}
	if !sawLog || !sawAlarm {
		t.Fatalf("expected log + alarm lines: %+v", res)
	}
}

func TestRetentionDefault(t *testing.T) {
	if RetentionDays(config.OpsConfig{}) != DefaultLogRetentionDays {
		t.Fatalf("default retention = %d", RetentionDays(config.OpsConfig{}))
	}
}
