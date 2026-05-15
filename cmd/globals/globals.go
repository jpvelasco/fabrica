package globals

import (
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

var (
	Cfg        *config.Config
	Provider   cloud.Provider
	Verbose    bool
	JSONOutput bool
	DryRun     bool
	Profile    string
	AssumeYes  bool
)
