package state

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/config"
)

const unresolvedAccountID = "<account-id>"

// BackendNames are the concrete S3/DynamoDB names for the state backend.
type BackendNames struct {
	Bucket string
	Table  string
}

// ResourcePlan describes one state-backend resource Fabrica will manage.
type ResourcePlan struct {
	TypeName   string
	Kind       string
	Identifier string
}

// SetupPlan is the provider/account-specific state-backend plan.
type SetupPlan struct {
	Account   string
	Region    string
	Backend   BackendNames
	Resources []ResourcePlan
}

// DefaultBucket returns the default state bucket name for an AWS account.
func DefaultBucket(account string) string {
	if account == "" {
		account = unresolvedAccountID
	}
	return fmt.Sprintf("fabrica-state-%s", account)
}

// ResolveBackendNames returns configured state names with defaults applied.
func ResolveBackendNames(cfg *config.Config, account string) BackendNames {
	names := BackendNames{
		Bucket: DefaultBucket(account),
		Table:  config.DefaultStateTable,
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

// ApplyBackendNames writes resolved state-backend names back into cfg.
func ApplyBackendNames(cfg *config.Config, account string) BackendNames {
	names := ResolveBackendNames(cfg, account)
	if cfg != nil {
		cfg.State.Bucket = names.Bucket
		cfg.State.Table = names.Table
	}
	return names
}

// NewSetupPlan builds the Phase 0 state-backend resource plan.
func NewSetupPlan(cfg *config.Config, account, region string) SetupPlan {
	backend := ApplyBackendNames(cfg, account)
	return SetupPlan{
		Account: account,
		Region:  region,
		Backend: backend,
		Resources: []ResourcePlan{
			{
				TypeName:   "AWS::S3::Bucket",
				Kind:       "S3 bucket",
				Identifier: backend.Bucket,
			},
			{
				TypeName:   "AWS::DynamoDB::Table",
				Kind:       "DynamoDB table",
				Identifier: backend.Table,
			},
		},
	}
}
