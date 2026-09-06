// Package ops is the offline plan layer for optional observability hooks.
// V1 writes local dashboard / log / alarm export files; it does not create
// CloudWatch resources. CostResources prices the standing lines operators
// would pay if they imported the hooks.
package ops

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

const (
	// TypeAWSLogsLogGroup is the CloudWatch Logs group type used for cost.
	TypeAWSLogsLogGroup = "AWS::Logs::LogGroup"

	DefaultLogRetentionDays = 30
	logGroupMonthly         = 0.50
)

// SupportedModules are the provisioned surfaces V1 can emit hooks for.
var SupportedModules = []string{"perforce", "horde", "ddc", "ci", "deploy"}

// Hook is one exported observability surface for a module.
type Hook struct {
	Module    string `json:"module"`
	LogGroup  string `json:"logGroup"`
	Alarm     string `json:"alarm"`
	Namespace string `json:"namespace"`
	Metric    string `json:"metric"`
}

// Export is the local dashboard + hook document `ops export` writes.
type Export struct {
	Title            string   `json:"title"`
	LogRetentionDays int      `json:"logRetentionDays"`
	Modules          []string `json:"modules"`
	Hooks            []Hook   `json:"hooks"`
	Note             string   `json:"note"`
}

// ResolveModules returns the enabled module list. An empty config list means
// every supported module. Unknown names are rejected so a typo cannot silently
// drop a hook.
func ResolveModules(cfg config.OpsConfig) ([]string, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if len(cfg.Modules) == 0 {
		out := make([]string, len(SupportedModules))
		copy(out, SupportedModules)
		return out, nil
	}
	allowed := make(map[string]bool, len(SupportedModules))
	for _, name := range SupportedModules {
		allowed[name] = true
	}
	seen := make(map[string]bool, len(cfg.Modules))
	out := make([]string, 0, len(cfg.Modules))
	for _, name := range cfg.Modules {
		name = strings.ToLower(strings.TrimSpace(name))
		if !allowed[name] {
			return nil, fmt.Errorf("ops.modules: unknown module %q (want one of %s)", name, strings.Join(SupportedModules, ", "))
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// RetentionDays returns the configured log retention, defaulting to 30.
func RetentionDays(cfg config.OpsConfig) int {
	if cfg.LogRetentionDays <= 0 {
		return DefaultLogRetentionDays
	}
	return cfg.LogRetentionDays
}

// BuildExport builds the local hook document for the resolved modules.
func BuildExport(cfg config.OpsConfig) (Export, error) {
	modules, err := ResolveModules(cfg)
	if err != nil {
		return Export{}, err
	}
	hooks := make([]Hook, 0, len(modules))
	for _, name := range modules {
		hooks = append(hooks, Hook{
			Module:    name,
			LogGroup:  "/fabrica/" + name,
			Alarm:     "fabrica-" + name + "-unhealthy",
			Namespace: "Fabrica/" + strings.ToUpper(name[:1]) + name[1:],
			Metric:    "Unhealthy",
		})
	}
	return Export{
		Title:            "Fabrica ops",
		LogRetentionDays: RetentionDays(cfg),
		Modules:          modules,
		Hooks:            hooks,
		Note:             "Local export only — import these log groups and alarms into CloudWatch or Grafana. Fabrica does not provision them.",
	}, nil
}

// CostResources returns standing CloudWatch lines when ops is enabled.
func CostResources(cfg config.OpsConfig) []cost.Resource {
	modules, err := ResolveModules(cfg)
	if err != nil || len(modules) == 0 {
		return nil
	}
	out := make([]cost.Resource, 0, len(modules)*2)
	for _, name := range modules {
		out = append(out,
			cost.Resource{TypeName: cloud.TypeAWSCloudWatchAlarm, Name: "fabrica-" + name + "-unhealthy"},
			cost.Resource{TypeName: TypeAWSLogsLogGroup, Name: "/fabrica/" + name},
		)
	}
	return out
}

type logGroupEstimator struct{}

func (logGroupEstimator) Estimate(r cost.Resource) (cost.Monthly, error) {
	return cost.Monthly{
		Amount:     logGroupMonthly,
		Confidence: cost.Medium,
		Note:       fmt.Sprintf("%s (CloudWatch Logs, low ingestion)", r.Name),
	}, nil
}

func init() {
	cost.Global.Register(TypeAWSLogsLogGroup, logGroupEstimator{})
}
