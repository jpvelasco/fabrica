package globals

import (
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

var (
	Cfg        *config.Config
	Provider   cloud.Provider
	ConfigPath string
	Verbose    bool
	JSONOutput bool
	DryRun     bool
	Profile    string
	AssumeYes  bool
)

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

	ConfigPath = path
	Cfg = cfg
	Provider = provider
	return nil
}

// ConfigFile returns the concrete path used for write-back and diagnostics.
func ConfigFile() string {
	if ConfigPath == "" {
		return config.DefaultFile
	}
	return ConfigPath
}
