package state

import (
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

const unresolvedAccountID = "<account-id>"

const (
	stateBucketTypeName = "AWS::S3::Bucket"
	lockTableTypeName   = "AWS::DynamoDB::Table"
)

// BackendNames are the concrete S3/DynamoDB names for the state backend.
type BackendNames struct {
	Bucket string
	Table  string
}

// ResourcePlan describes one state-backend resource that setup will create.
type ResourcePlan struct {
	TypeName   string
	Label      string
	Identifier string
}

// SetupPlan is the resolved Phase 0 state-backend plan for one account/region.
type SetupPlan struct {
	Prefix    naming.Prefix
	Account   string
	Region    string
	Backend   BackendNames
	Resources []ResourcePlan
}

// ConfiguredBackendNames resolves the state names for the config's name
// prefix. It fails when the configured prefix is not yet supported.
func ConfiguredBackendNames(cfg *config.Config, account string) (BackendNames, error) {
	prefix, err := cfg.ResolveNamePrefix()
	if err != nil {
		return BackendNames{}, err
	}
	return ResolveBackendNames(prefix, cfg, account), nil
}

// ResolveBackendNames returns configured state names with defaults derived from
// the name prefix. Explicit state.bucket/state.table always win.
func ResolveBackendNames(prefix naming.Prefix, cfg *config.Config, account string) BackendNames {
	if account == "" {
		account = unresolvedAccountID
	}
	names := BackendNames{
		Bucket: prefix.StateBucket(account),
		Table:  prefix.StateTable(),
	}
	if cfg == nil {
		return names
	}
	if cfg.State.Bucket != "" {
		names.Bucket = cfg.State.Bucket
	}
	if cfg.State.Table != "" {
		names.Table = cfg.State.Table
	}
	return names
}

// NewSetupPlan builds the Phase 0 state-backend resource plan.
func NewSetupPlan(prefix naming.Prefix, cfg *config.Config, account, region string) SetupPlan {
	backend := ResolveBackendNames(prefix, cfg, account)
	return SetupPlan{
		Prefix:  prefix,
		Account: account,
		Region:  region,
		Backend: backend,
		Resources: []ResourcePlan{
			{
				TypeName:   stateBucketTypeName,
				Label:      "S3 bucket",
				Identifier: backend.Bucket,
			},
			{
				TypeName:   lockTableTypeName,
				Label:      "DynamoDB table",
				Identifier: backend.Table,
			},
		},
	}
}
