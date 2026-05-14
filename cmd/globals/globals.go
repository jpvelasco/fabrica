package globals

import "github.com/jpvelasco/fabrica/internal/config"

var (
	Cfg        *config.Config
	Verbose    bool
	JSONOutput bool
	DryRun     bool
	Profile    string
	AssumeYes  bool
)
