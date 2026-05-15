package globals

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

// Runtime is the command dependency set initialized by the root command.
type Runtime struct {
	Config     *config.Config
	Provider   cloud.Provider
	ConfigPath string
}

var (
	Verbose    bool
	JSONOutput bool
	DryRun     bool
	Profile    string
	AssumeYes  bool
)

var current Runtime

// Init loads configuration and resolves the configured cloud provider.
func Init(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	provider, err := cloud.Get(cfg.Cloud.Provider, cfg)
	if err != nil {
		return err
	}

	current = Runtime{
		Config:     cfg,
		Provider:   provider,
		ConfigPath: path,
	}
	return nil
}

// Current returns the initialized runtime dependency set.
func Current() Runtime {
	return current
}

// RequireCurrent returns the runtime or an error if root initialization failed.
func RequireCurrent() (Runtime, error) {
	if current.Config == nil {
		return Runtime{}, fmt.Errorf("no configuration loaded")
	}
	if current.Provider == nil {
		return Runtime{}, fmt.Errorf("no cloud provider loaded")
	}
	return current, nil
}

// ConfigFile returns the concrete path used for write-back and diagnostics.
func (r Runtime) ConfigFile() string {
	if r.ConfigPath == "" {
		return config.DefaultFile
	}
	return r.ConfigPath
}
