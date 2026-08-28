package lore

import (
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewCreatePlanMissingAmiID(t *testing.T) {
	cfg := config.LoreConfig{}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when AmiID is empty")
	}
	assert.Contains(t, err.Error(), "lore.amiId is required")
	assert.Contains(t, err.Error(), "docs/lore-ami.md")
}

func TestNewCreatePlanDefaults(t *testing.T) {
	cfg := config.LoreConfig{AmiID: "ami-abc123"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != "m5.xlarge" {
		t.Errorf("InstanceType = %q, want m5.xlarge", plan.InstanceType)
	}
	if plan.VolumeSize != 500 {
		t.Errorf("VolumeSize = %d, want 500", plan.VolumeSize)
	}
	if plan.GRPCPort != DefaultGRPCPort {
		t.Errorf("GRPCPort = %d, want %d", plan.GRPCPort, DefaultGRPCPort)
	}
	if plan.HTTPPort != DefaultHTTPPort {
		t.Errorf("HTTPPort = %d, want %d", plan.HTTPPort, DefaultHTTPPort)
	}
	if plan.AllowedCIDR != "10.0.0.0/8" {
		t.Errorf("AllowedCIDR = %q, want 10.0.0.0/8", plan.AllowedCIDR)
	}
	if plan.SGName != "fabrica-lore-sg" {
		t.Errorf("SGName = %q, want fabrica-lore-sg", plan.SGName)
	}
	if plan.InstanceName != "fabrica-lore" {
		t.Errorf("InstanceName = %q, want fabrica-lore", plan.InstanceName)
	}
	if plan.AmiID != "ami-abc123" {
		t.Errorf("AmiID = %q, want ami-abc123", plan.AmiID)
	}
	// Default store backend is local.
	if plan.StoreBackend != StoreBackendLocal {
		t.Errorf("StoreBackend = %q, want local", plan.StoreBackend)
	}
	if plan.StoreBucket != "" {
		t.Errorf("StoreBucket = %q, want empty for local backend", plan.StoreBucket)
	}
}

func TestNewCreatePlanS3StoreBackend(t *testing.T) {
	cfg := config.LoreConfig{
		AmiID:        "ami-abc123",
		StoreBackend: "s3",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.StoreBackend != StoreBackendS3 {
		t.Errorf("StoreBackend = %q, want s3", plan.StoreBackend)
	}
	if plan.StoreBucket != "fabrica-lore-store-123456789012-us-east-1" {
		t.Errorf("StoreBucket = %q, want fabrica-lore-store-123456789012-us-east-1", plan.StoreBucket)
	}
	if plan.RoleName != "fabrica-lore-role" {
		t.Errorf("RoleName = %q, want fabrica-lore-role", plan.RoleName)
	}
	if plan.InstanceProfileName != "fabrica-lore-profile" {
		t.Errorf("InstanceProfileName = %q, want fabrica-lore-profile", plan.InstanceProfileName)
	}
}

func TestNewCreatePlanS3StoreCustomBucket(t *testing.T) {
	cfg := config.LoreConfig{
		AmiID:        "ami-abc123",
		StoreBackend: "s3",
		StoreBucket:  "my-lore-bucket",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.StoreBucket != "my-lore-bucket" {
		t.Errorf("StoreBucket = %q, want my-lore-bucket", plan.StoreBucket)
	}
}

func TestNewCreatePlanS3StoreTables(t *testing.T) {
	cfg := config.LoreConfig{
		AmiID:        "ami-abc123",
		StoreBackend: "s3",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"fabrica-lore-store-123456789012-us-east-1-fragments",
		"fabrica-lore-store-123456789012-us-east-1-metadata",
		"fabrica-lore-store-123456789012-us-east-1-mutable",
		"fabrica-lore-store-123456789012-us-east-1-locks",
	}
	if len(plan.StoreTables) != len(want) {
		t.Fatalf("StoreTables = %v, want %v", plan.StoreTables, want)
	}
	for i, w := range want {
		if plan.StoreTables[i] != w {
			t.Errorf("StoreTables[%d] = %q, want %q", i, plan.StoreTables[i], w)
		}
	}
}

func TestNewCreatePlanLocalStoreNoTables(t *testing.T) {
	cfg := config.LoreConfig{AmiID: "ami-abc123"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.StoreTables) != 0 {
		t.Errorf("StoreTables = %v, want empty for local backend", plan.StoreTables)
	}
}

func TestStoreTableNamesDeriveFromBucket(t *testing.T) {
	got := StoreTableNames("my-lore-bucket")
	want := []string{
		"my-lore-bucket-fragments",
		"my-lore-bucket-metadata",
		"my-lore-bucket-mutable",
		"my-lore-bucket-locks",
	}
	if len(got) != len(want) {
		t.Fatalf("StoreTableNames = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("StoreTableNames[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestStoreDynamoDBPolicyRegionAccountFallbacks covers the partition-agnostic
// fallback when region/account are unset (test callers that build the policy
// without a live state).
func TestStoreDynamoDBPolicyRegionAccountFallbacks(t *testing.T) {
	pol := StoreDynamoDBPolicy("", "", "bkt", []string{"bkt-fragments", "bkt-locks"})
	if pol["PolicyName"] != "fabrica-lore-store-dynamodb" {
		t.Fatalf("PolicyName = %v, want fabrica-lore-store-dynamodb", pol["PolicyName"])
	}
	doc := pol["PolicyDocument"].(map[string]any)
	stmt := doc["Statement"].([]map[string]any)[0]
	actions := stmt["Action"].([]string)
	if len(actions) != 7 || actions[0] != "dynamodb:GetItem" {
		t.Errorf("statement actions = %v, want the seven store actions", actions)
	}
	res := stmt["Resource"].([]string)
	want := []string{
		"arn:aws:dynamodb:*:*:table/bkt-fragments",
		"arn:aws:dynamodb:*:*:table/bkt-locks",
		"arn:aws:dynamodb:*:*:table/bkt-locks/index/*",
	}
	if len(res) != len(want) {
		t.Fatalf("statement resources = %v, want %v", res, want)
	}
	for i, w := range want {
		if res[i] != w {
			t.Errorf("resource[%d] = %q, want %q", i, res[i], w)
		}
	}
}

func TestStoreTableSpecsMatchPluginSchema(t *testing.T) {
	tables := StoreTables()
	if len(tables) != 4 {
		t.Fatalf("StoreTables len = %d, want 4", len(tables))
	}

	// fragments: PK hash (B) + SK repository_context (B), no GSIs.
	f := tables[0]
	if f.Suffix != "fragments" || f.PK != "hash" || f.PKType != "B" {
		t.Errorf("fragments key = %s (%s), want hash (B)", f.PK, f.PKType)
	}
	if f.SK != "repository_context" || f.SKType != "B" {
		t.Errorf("fragments sort key = %s (%s), want repository_context (B)", f.SK, f.SKType)
	}
	if len(f.GSIs) != 0 {
		t.Errorf("fragments GSIs = %v, want none", f.GSIs)
	}

	// metadata: PK hash (B) only.
	m := tables[1]
	if m.Suffix != "metadata" || m.PK != "hash" || m.PKType != "B" {
		t.Errorf("metadata key = %s (%s), want hash (B)", m.PK, m.PKType)
	}
	if m.SK != "" || len(m.GSIs) != 0 {
		t.Errorf("metadata SK/GSIs = %q/%v, want none", m.SK, m.GSIs)
	}

	// mutable: PK repository_id (B) + SK key (B).
	um := tables[2]
	if um.Suffix != "mutable" || um.PK != "repository_id" || um.PKType != "B" {
		t.Errorf("mutable key = %s (%s), want repository_id (B)", um.PK, um.PKType)
	}
	if um.SK != "key" || um.SKType != "B" {
		t.Errorf("mutable sort key = %s (%s), want key (B)", um.SK, um.SKType)
	}

	// locks: PK hash (B) + SK repositoryBranch (B), three GSIs.
	l := tables[3]
	if l.Suffix != "locks" || l.PK != "hash" || l.PKType != "B" {
		t.Errorf("locks key = %s (%s), want hash (B)", l.PK, l.PKType)
	}
	if l.SK != "repositoryBranch" || l.SKType != "B" {
		t.Errorf("locks sort key = %s (%s), want repositoryBranch (B)", l.SK, l.SKType)
	}
	if len(l.GSIs) != 3 {
		t.Fatalf("locks GSIs len = %d, want 3", len(l.GSIs))
	}
	wantGSIs := []GSI{
		{Name: "owner-repo-branch", PK: "ownerId", PKType: "S", SK: "repositoryBranch", SKType: "B"},
		{Name: "repo-branch", PK: "repository", PKType: "B", SK: "branch", SKType: "B"},
		{Name: "repo-branch-description", PK: "repositoryBranch", PKType: "B", SK: "description", SKType: "S"},
	}
	for i, w := range wantGSIs {
		g := l.GSIs[i]
		if g != w {
			t.Errorf("locks GSI[%d] = %+v, want %+v", i, g, w)
		}
	}
}

func TestNewCreatePlanInvalidStoreBackend(t *testing.T) {
	cfg := config.LoreConfig{
		AmiID:        "ami-abc123",
		StoreBackend: "invalid",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid backend should fall back to local.
	if plan.StoreBackend != StoreBackendLocal {
		t.Errorf("StoreBackend = %q, want local for invalid input", plan.StoreBackend)
	}
}

func TestNewCreatePlanTLSConfig(t *testing.T) {
	cfg := config.LoreConfig{
		AmiID: "ami-abc123",
		TLSConfig: config.LoreTLSConfig{
			Enabled:  true,
			CertPath: "/etc/ssl/certs/lore.crt",
			KeyPath:  "/etc/ssl/private/lore.key",
		},
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !plan.TLSConfig.Enabled {
		t.Error("TLSConfig.Enabled should be true")
	}
	if plan.TLSConfig.CertPath != "/etc/ssl/certs/lore.crt" {
		t.Errorf("TLSConfig.CertPath = %q", plan.TLSConfig.CertPath)
	}
}

func TestNewCreatePlanExplicitValues(t *testing.T) {
	cfg := config.LoreConfig{
		AmiID:        "ami-abc123",
		InstanceType: "m5.2xlarge",
		VolumeSize:   1000,
		AllowedCIDR:  "0.0.0.0/0",
		VPCId:        "vpc-explicit",
		SubnetId:     "subnet-explicit",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != "m5.2xlarge" {
		t.Errorf("InstanceType = %q, want m5.2xlarge", plan.InstanceType)
	}
	if plan.VolumeSize != 1000 {
		t.Errorf("VolumeSize = %d, want 1000", plan.VolumeSize)
	}
	if plan.AllowedCIDR != "0.0.0.0/0" {
		t.Errorf("AllowedCIDR = %q, want 0.0.0.0/0", plan.AllowedCIDR)
	}
	if plan.DefaultVPC {
		t.Error("DefaultVPC should be false when VPC is explicit")
	}
	if plan.VPCID != "vpc-explicit" || plan.SubnetID != "subnet-explicit" {
		t.Errorf("VPC/subnet = %s/%s", plan.VPCID, plan.SubnetID)
	}
}

func TestNewCreatePlanVPCResolver(t *testing.T) {
	cfg := config.LoreConfig{AmiID: "ami-abc123"}
	resolver := &cloud.TestVPCResolver{VPCID: "vpc-fake", SubnetID: "subnet-fake"}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !plan.DefaultVPC {
		t.Error("DefaultVPC should be true when resolver fills VPC")
	}
	if plan.VPCID != "vpc-fake" || plan.SubnetID != "subnet-fake" {
		t.Errorf("VPC/subnet = %s/%s", plan.VPCID, plan.SubnetID)
	}
}

func TestNewCreatePlanVPCResolverError(t *testing.T) {
	cfg := config.LoreConfig{AmiID: "ami-abc123"}
	resolver := &cloud.TestVPCResolver{Err: errFakeVPC}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err == nil {
		t.Fatal("expected error from resolver")
	}
	assert.Contains(t, err.Error(), "resolving default VPC")
}

var errFakeVPC = errString("no default VPC")

type errString string

func (e errString) Error() string { return string(e) }
