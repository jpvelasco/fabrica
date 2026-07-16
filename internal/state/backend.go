package state

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/config"
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

// NewSetupPlan builds the Phase 0 state-backend resource plan.
func NewSetupPlan(cfg *config.Config, account, region string) SetupPlan {
	backend := ResolveBackendNames(cfg, account)
	return SetupPlan{
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
