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

// Options are the root flags used by subcommands.
type Options struct {
	Verbose    bool
	JSONOutput bool
	DryRun     bool
	Profile    string
	AssumeYes  bool
}

// RuntimeSource returns the initialized command runtime.
type RuntimeSource func() (Runtime, error)

// OptionsSource returns the current root options.
type OptionsSource func() Options

// Store owns the runtime initialized by the root command.
type Store struct {
	current Runtime
}

// Init loads configuration and resolves the configured cloud provider.
func (s *Store) Init(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	provider, err := cloud.Get(cfg.Cloud.Provider, cfg)
	if err != nil {
		return err
	}

	s.current = Runtime{
		Config:     cfg,
		Provider:   provider,
		ConfigPath: path,
	}
	return nil
}

// Require returns the runtime or an error if root initialization failed.
func (s *Store) Require() (Runtime, error) {
	if s.current.Config == nil {
		return Runtime{}, fmt.Errorf("no configuration loaded")
	}
	if s.current.Provider == nil {
		return Runtime{}, fmt.Errorf("no cloud provider loaded")
	}
	return s.current, nil
}

// ConfigFile returns the concrete path used for write-back and diagnostics.
func (r Runtime) ConfigFile() string {
	if r.ConfigPath == "" {
		return config.DefaultFile
	}
	return r.ConfigPath
}
