# Virtual Workstations V1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `fabrica workstation create` and `fabrica workstation list` commands that provision a NICE DCV cloud workstation on EC2, following the exact perforce/horde module pattern.

**Architecture:** New `internal/workstation/` pure plan layer (no AWS SDK) + `cmd/workstation/` Cobra layer. `WorkstationConfig` lives in `internal/config/config.go`. GPU instance prices are added to the existing `ec2InstancePrices` map in `internal/perforce/cost.go` — no new cost registrations (AWS::EC2::Instance and AWS::EC2::Volume are already registered there). DCV port 8443 is probed for readiness.

**Tech Stack:** Go, Cobra/Viper, AWS Cloud Control API (`aws-sdk-go-v2/service/cloudcontrol`), NICE DCV (AMI-first, user provides AMI ID), cloud-init for DCV startup.

---

## Scope check

This plan covers the first PR scope from the handoff doc: `workstation create` + `workstation list`. Stop/start/terminate are future PRs. Each task is independently committable.

---

## File Map

**New files:**
- `internal/workstation/config.go` — `VPCResolver` interface + constants
- `internal/workstation/plan.go` — `CreatePlan` struct + `NewCreatePlan()`
- `internal/workstation/plan_test.go` — tests for `NewCreatePlan`
- `internal/workstation/resources.go` — `SGDesiredState()`, `InstanceDesiredState()`
- `internal/workstation/resources_test.go` — tests for desired-state builders
- `internal/workstation/userdata.go` — `Generate()`, `GenerateRaw()` for cloud-init
- `internal/workstation/userdata_test.go` — tests for cloud-init rendering
- `cmd/workstation/workstation.go` — parent command
- `cmd/workstation/create/create.go` — create subcommand
- `cmd/workstation/create/create_test.go` — white-box tests
- `cmd/workstation/create/cobra_test.go` — black-box Cobra tests
- `cmd/workstation/list/list.go` — list subcommand
- `cmd/workstation/list/list_test.go` — white-box tests
- `cmd/workstation/list/cobra_test.go` — black-box Cobra tests

**Modified files:**
- `internal/config/config.go` — add `WorkstationConfig` struct + `Workstation` field on `Config`
- `internal/perforce/cost.go` — add GPU instance prices to `ec2InstancePrices` map
- `cmd/root/root.go` — register `workstation.New(...)` command
- `AGENTS.md` — add workstation row to module table

---

## Task 1: Add `WorkstationConfig` to config

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Write the failing test**

In `internal/config/config_test.go`, add:

```go
func TestWorkstationConfigDefaults(t *testing.T) {
    cfg := Defaults()
    if cfg.Workstation.InstanceType != "" {
        t.Errorf("expected empty InstanceType default, got %q", cfg.Workstation.InstanceType)
    }
}

func TestWorkstationConfigUnmarshal(t *testing.T) {
    yaml := `
workstation:
  amiId: ami-12345678
  instanceType: g4dn.xlarge
  volumeSize: 200
  vpcId: vpc-abc
  subnetId: subnet-def
  idleTimeoutMinutes: 30
  allowedCidr: 10.0.0.0/8
`
    v := viper.New()
    v.SetConfigType("yaml")
    v.ReadConfig(strings.NewReader(yaml))
    cfg := Defaults()
    if err := v.Unmarshal(cfg); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if cfg.Workstation.AmiID != "ami-12345678" {
        t.Errorf("AmiID = %q, want ami-12345678", cfg.Workstation.AmiID)
    }
    if cfg.Workstation.InstanceType != "g4dn.xlarge" {
        t.Errorf("InstanceType = %q, want g4dn.xlarge", cfg.Workstation.InstanceType)
    }
    if cfg.Workstation.VolumeSize != 200 {
        t.Errorf("VolumeSize = %d, want 200", cfg.Workstation.VolumeSize)
    }
    if cfg.Workstation.IdleTimeoutMinutes != 30 {
        t.Errorf("IdleTimeoutMinutes = %d, want 30", cfg.Workstation.IdleTimeoutMinutes)
    }
    if cfg.Workstation.AllowedCIDR != "10.0.0.0/8" {
        t.Errorf("AllowedCIDR = %q, want 10.0.0.0/8", cfg.Workstation.AllowedCIDR)
    }
}
```

Note: `config_test.go` already imports `strings` and `viper` — check before adding imports.

- [ ] **Step 2: Run the test to confirm it fails**

```bash
go test ./internal/config/... -run TestWorkstationConfig -v
```

Expected: compile error — `WorkstationConfig` undefined.

- [ ] **Step 3: Add `WorkstationConfig` to `internal/config/config.go`**

Add after the `HordeConfig` struct (around line 80):

```go
// WorkstationConfig holds the workstation: section of fabrica.yaml.
type WorkstationConfig struct {
	AmiID              string `mapstructure:"amiId"              yaml:"amiId"`
	InstanceType       string `mapstructure:"instanceType"       yaml:"instanceType"`
	VolumeSize         int    `mapstructure:"volumeSize"         yaml:"volumeSize"`
	VPCId              string `mapstructure:"vpcId"              yaml:"vpcId"`
	SubnetId           string `mapstructure:"subnetId"           yaml:"subnetId"`
	IdleTimeoutMinutes int    `mapstructure:"idleTimeoutMinutes" yaml:"idleTimeoutMinutes"`
	AllowedCIDR        string `mapstructure:"allowedCidr"        yaml:"allowedCidr"`
}
```

Add `Workstation WorkstationConfig` to the `Config` struct (after `Horde`):

```go
type Config struct {
	Cloud       Cloud             `mapstructure:"cloud"       yaml:"cloud"`
	State       State             `mapstructure:"state"       yaml:"state"`
	Perforce    PerforceConfig    `mapstructure:"perforce"    yaml:"perforce"`
	Horde       HordeConfig       `mapstructure:"horde"       yaml:"horde"`
	Workstation WorkstationConfig `mapstructure:"workstation" yaml:"workstation"`
	CI          any               `mapstructure:"ci"          yaml:"ci"`
	Cost        any               `mapstructure:"cost"        yaml:"cost"`
}
```

Also add `Workstation WorkstationConfig` to `fileConfig` struct and its `fileConfig()` method in the same file:

```go
type fileConfig struct {
	Cloud       Cloud             `yaml:"cloud"`
	State       State             `yaml:"state"`
	Perforce    PerforceConfig    `yaml:"perforce"`
	Horde       HordeConfig       `yaml:"horde"`
	Workstation WorkstationConfig `yaml:"workstation"`
	CI          any               `yaml:"ci"`
	Cost        any               `yaml:"cost"`
}

func (c *Config) fileConfig() fileConfig {
	return fileConfig{
		Cloud:       c.Cloud,
		State:       c.State,
		Perforce:    c.Perforce,
		Horde:       c.Horde,
		Workstation: c.Workstation,
		CI:          emptySection(c.CI),
		Cost:        emptySection(c.Cost),
	}
}
```

- [ ] **Step 4: Run the test to confirm it passes**

```bash
go test ./internal/config/... -run TestWorkstationConfig -v
```

Expected: PASS

- [ ] **Step 5: Run the full config test suite**

```bash
go test ./internal/config/... -v
```

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add WorkstationConfig to internal/config"
```

---

## Task 2: Add GPU instance prices to cost estimator

**Files:**
- Modify: `internal/perforce/cost.go`

The `AWS::EC2::Instance` estimator is already registered. We only need to add GPU instance types to the price table so `NewCreatePlan` can include cost resources for workstations.

- [ ] **Step 1: Write the failing test**

In `internal/perforce/cost_test.go`, add:

```go
func TestGPUInstancePrices(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		want float64
	}{
		{"g4dn.xlarge", 0.526},
		{"g4dn.2xlarge", 0.752},
		{"g5.xlarge", 1.006},
		{"g5.2xlarge", 1.212},
	} {
		r := cost.Resource{TypeName: TypeAWSEC2Instance, Name: tc.typ}
		got, err := cost.Global.Estimate(TypeAWSEC2Instance, r)
		if err != nil {
			t.Errorf("%s: %v", tc.typ, err)
			continue
		}
		if got.Amount < tc.want*0.99 || got.Amount > tc.want*1.01 {
			t.Errorf("%s: amount = %.4f, want ~%.4f", tc.typ, got.Amount, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to confirm it fails**

```bash
go test ./internal/perforce/... -run TestGPUInstancePrices -v
```

Expected: FAIL — no price data for g4dn.xlarge.

- [ ] **Step 3: Add GPU instance prices to `ec2InstancePrices` in `internal/perforce/cost.go`**

Add these entries to the existing `ec2InstancePrices` map (after the m7i entries):

```go
// GPU instances for cloud workstations (us-east-1, Linux, on-demand, 2024-Q4).
"g4dn.xlarge":  0.526,
"g4dn.2xlarge": 0.752,
"g4dn.4xlarge": 1.204,
"g4dn.8xlarge": 2.264,
"g5.xlarge":    1.006,
"g5.2xlarge":   1.212,
"g5.4xlarge":   1.624,
"g5.8xlarge":   2.448,
```

- [ ] **Step 4: Run the test to confirm it passes**

```bash
go test ./internal/perforce/... -run TestGPUInstancePrices -v
```

Expected: PASS

- [ ] **Step 5: Run full perforce test suite**

```bash
go test ./internal/perforce/... -v
```

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/perforce/cost.go internal/perforce/cost_test.go
git commit -m "feat: add GPU instance prices for workstation cost estimation"
```

---

## Task 3: `internal/workstation/config.go` — VPCResolver interface

**Files:**
- Create: `internal/workstation/config.go`

- [ ] **Step 1: Create the file**

```go
package workstation

import "context"

const (
	DefaultInstanceType       = "g4dn.xlarge"
	DefaultVolumeSize         = 100
	DefaultDCVPort            = 8443
	DefaultIdleTimeoutMinutes = 60
	DefaultAllowedCIDR        = "0.0.0.0/0"
)

// VPCResolver resolves VPC and subnet IDs. The AWS provider implements this
// via ec2:DescribeVpcs so that internal/workstation stays free of AWS SDK imports.
type VPCResolver interface {
	ResolveDefaultVPC(ctx context.Context) (vpcID, subnetID string, err error)
}
```

- [ ] **Step 2: Build to confirm it compiles**

```bash
go build ./internal/workstation/...
```

Expected: success (no errors)

- [ ] **Step 3: Commit**

```bash
git add internal/workstation/config.go
git commit -m "feat: add internal/workstation package skeleton with VPCResolver"
```

---

## Task 4: `internal/workstation/plan.go` — CreatePlan + NewCreatePlan

**Files:**
- Create: `internal/workstation/plan.go`
- Create: `internal/workstation/plan_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/workstation/plan_test.go`:

```go
package workstation

import (
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

type fakeVPCResolver struct {
	vpcID    string
	subnetID string
	err      error
}

func (f *fakeVPCResolver) ResolveDefaultVPC(_ context.Context) (string, string, error) {
	return f.vpcID, f.subnetID, f.err
}

func TestNewCreatePlanRequiresAmiID(t *testing.T) {
	cfg := config.WorkstationConfig{}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when AmiID is empty")
	}
	if !containsStr(err.Error(), "workstation.amiId") {
		t.Errorf("error %q should mention workstation.amiId", err.Error())
	}
}

func TestNewCreatePlanDefaults(t *testing.T) {
	cfg := config.WorkstationConfig{AmiID: "ami-abc123"}
	resolver := &fakeVPCResolver{vpcID: "vpc-default", subnetID: "subnet-default"}

	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != DefaultInstanceType {
		t.Errorf("InstanceType = %q, want %q", plan.InstanceType, DefaultInstanceType)
	}
	if plan.VolumeSize != DefaultVolumeSize {
		t.Errorf("VolumeSize = %d, want %d", plan.VolumeSize, DefaultVolumeSize)
	}
	if plan.DCVPort != DefaultDCVPort {
		t.Errorf("DCVPort = %d, want %d", plan.DCVPort, DefaultDCVPort)
	}
	if plan.IdleTimeoutMinutes != DefaultIdleTimeoutMinutes {
		t.Errorf("IdleTimeoutMinutes = %d, want %d", plan.IdleTimeoutMinutes, DefaultIdleTimeoutMinutes)
	}
	if plan.VPCID != "vpc-default" {
		t.Errorf("VPCID = %q, want vpc-default", plan.VPCID)
	}
	if plan.DefaultVPC != true {
		t.Error("DefaultVPC should be true when resolver was used")
	}
	if plan.SGName != "fabrica-workstation-sg" {
		t.Errorf("SGName = %q, want fabrica-workstation-sg", plan.SGName)
	}
	if plan.InstanceName != "fabrica-workstation" {
		t.Errorf("InstanceName = %q, want fabrica-workstation", plan.InstanceName)
	}
	if len(plan.CostResources) != 2 {
		t.Errorf("CostResources len = %d, want 2", len(plan.CostResources))
	}
}

func TestNewCreatePlanExplicitVPC(t *testing.T) {
	cfg := config.WorkstationConfig{
		AmiID:    "ami-abc123",
		VPCId:    "vpc-explicit",
		SubnetId: "subnet-explicit",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.VPCID != "vpc-explicit" {
		t.Errorf("VPCID = %q, want vpc-explicit", plan.VPCID)
	}
	if plan.DefaultVPC {
		t.Error("DefaultVPC should be false when VPC IDs are explicit")
	}
}

func TestNewCreatePlanVPCResolverError(t *testing.T) {
	cfg := config.WorkstationConfig{AmiID: "ami-abc123"}
	resolver := &fakeVPCResolver{err: errors.New("no default VPC")}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
	if err == nil {
		t.Fatal("expected error when resolver fails")
	}
	if !containsStr(err.Error(), "resolving default VPC") {
		t.Errorf("error %q should mention resolving default VPC", err.Error())
	}
}

func TestNewCreatePlanConfigOverrides(t *testing.T) {
	cfg := config.WorkstationConfig{
		AmiID:              "ami-abc123",
		InstanceType:       "g5.2xlarge",
		VolumeSize:         200,
		IdleTimeoutMinutes: 30,
		AllowedCIDR:        "10.0.0.0/8",
		VPCId:              "vpc-x",
		SubnetId:           "subnet-x",
	}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.InstanceType != "g5.2xlarge" {
		t.Errorf("InstanceType = %q, want g5.2xlarge", plan.InstanceType)
	}
	if plan.VolumeSize != 200 {
		t.Errorf("VolumeSize = %d, want 200", plan.VolumeSize)
	}
	if plan.IdleTimeoutMinutes != 30 {
		t.Errorf("IdleTimeoutMinutes = %d, want 30", plan.IdleTimeoutMinutes)
	}
	if plan.AllowedCIDR != "10.0.0.0/8" {
		t.Errorf("AllowedCIDR = %q, want 10.0.0.0/8", plan.AllowedCIDR)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run the test to confirm it fails**

```bash
go test ./internal/workstation/... -run TestNewCreatePlan -v
```

Expected: compile error — `NewCreatePlan` undefined.

- [ ] **Step 3: Create `internal/workstation/plan.go`**

```go
package workstation

import (
	"context"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

const (
	typeEC2Instance = "AWS::EC2::Instance"
	typeEC2Volume   = "AWS::EC2::Volume"
)

// CreatePlan holds everything needed to provision a workstation. No AWS SDK
// types — callers execute the plan via rt.Provider.Resources().
type CreatePlan struct {
	Account            string
	Region             string
	AmiID              string
	InstanceType       string
	VolumeSize         int
	DCVPort            int
	IdleTimeoutMinutes int
	AllowedCIDR        string
	MountPerforce      bool
	VPCID              string
	SubnetID           string
	DefaultVPC         bool

	SGName       string
	InstanceName string

	CostResources []cost.Resource
}

// NewCreatePlan validates inputs and builds a CreatePlan. VPCResolver is called
// only when VPCId/SubnetId are absent from cfg; pass nil to skip resolution.
func NewCreatePlan(ctx context.Context, cfg config.WorkstationConfig, account, region string, resolver VPCResolver) (*CreatePlan, error) {
	if cfg.AmiID == "" {
		return nil, fmt.Errorf("workstation.amiId is required. Provide a NICE DCV-enabled AMI ID.\nSee: https://docs.aws.amazon.com/dcv/latest/adminguide/setting-up-installing.html")
	}

	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = DefaultInstanceType
	}
	volumeSize := cfg.VolumeSize
	if volumeSize <= 0 {
		volumeSize = DefaultVolumeSize
	}
	dcvPort := DefaultDCVPort
	idleTimeout := cfg.IdleTimeoutMinutes
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeoutMinutes
	}
	allowedCIDR := cfg.AllowedCIDR
	if allowedCIDR == "" {
		allowedCIDR = DefaultAllowedCIDR
	}

	vpcID := cfg.VPCId
	subnetID := cfg.SubnetId
	defaultVPC := false
	if (vpcID == "" || subnetID == "") && resolver != nil {
		var err error
		vpcID, subnetID, err = resolver.ResolveDefaultVPC(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolving default VPC: %w", err)
		}
		defaultVPC = true
	}

	return &CreatePlan{
		Account:            account,
		Region:             region,
		AmiID:              cfg.AmiID,
		InstanceType:       instanceType,
		VolumeSize:         volumeSize,
		DCVPort:            dcvPort,
		IdleTimeoutMinutes: idleTimeout,
		AllowedCIDR:        allowedCIDR,
		VPCID:              vpcID,
		SubnetID:           subnetID,
		DefaultVPC:         defaultVPC,
		SGName:             "fabrica-workstation-sg",
		InstanceName:       "fabrica-workstation",
		CostResources: []cost.Resource{
			{TypeName: typeEC2Instance, Name: instanceType},
			{TypeName: typeEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
		},
	}, nil
}
```

- [ ] **Step 4: Run the tests to confirm they pass**

```bash
go test ./internal/workstation/... -run TestNewCreatePlan -v
```

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/workstation/plan.go internal/workstation/plan_test.go
git commit -m "feat: add workstation CreatePlan and NewCreatePlan"
```

---

## Task 5: `internal/workstation/resources.go` — Cloud Control desired-state builders

**Files:**
- Create: `internal/workstation/resources.go`
- Create: `internal/workstation/resources_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/workstation/resources_test.go`:

```go
package workstation

import (
	"encoding/json"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func testPlan(t *testing.T) *CreatePlan {
	t.Helper()
	plan, err := NewCreatePlan(nil, config.WorkstationConfig{
		AmiID:    "ami-abc123",
		VPCId:    "vpc-test",
		SubnetId: "subnet-test",
	}, "123456789012", "us-east-1", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	return plan
}

func TestSGDesiredStateFields(t *testing.T) {
	plan := testPlan(t)
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["GroupName"] != plan.SGName {
		t.Errorf("GroupName = %v, want %q", doc["GroupName"], plan.SGName)
	}
	if doc["VpcId"] != plan.VPCID {
		t.Errorf("VpcId = %v, want %q", doc["VpcId"], plan.VPCID)
	}
	ingress, ok := doc["SecurityGroupIngress"].([]any)
	if !ok || len(ingress) == 0 {
		t.Fatal("SecurityGroupIngress missing or empty")
	}
	rule := ingress[0].(map[string]any)
	if rule["FromPort"] != float64(DefaultDCVPort) {
		t.Errorf("FromPort = %v, want %d", rule["FromPort"], DefaultDCVPort)
	}
}

func TestSGDesiredStateManagedByTag(t *testing.T) {
	plan := testPlan(t)
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	if !containsStr(string(raw), "fabrica") {
		t.Error("SG desired state must contain ManagedBy=fabrica tag")
	}
}

func TestInstanceDesiredStateFields(t *testing.T) {
	plan := testPlan(t)
	raw, err := InstanceDesiredState(plan, "sg-test123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["ImageId"] != plan.AmiID {
		t.Errorf("ImageId = %v, want %q", doc["ImageId"], plan.AmiID)
	}
	if doc["InstanceType"] != plan.InstanceType {
		t.Errorf("InstanceType = %v, want %q", doc["InstanceType"], plan.InstanceType)
	}
	if doc["SubnetId"] != plan.SubnetID {
		t.Errorf("SubnetId = %v, want %q", doc["SubnetId"], plan.SubnetID)
	}
	sgs, ok := doc["SecurityGroupIds"].([]any)
	if !ok || len(sgs) != 1 || sgs[0] != "sg-test123" {
		t.Errorf("SecurityGroupIds = %v, want [sg-test123]", doc["SecurityGroupIds"])
	}
	meta, ok := doc["MetadataOptions"].(map[string]any)
	if !ok || meta["HttpTokens"] != "required" {
		t.Error("MetadataOptions.HttpTokens must be required (IMDSv2)")
	}
}

func TestInstanceDesiredStateVolume(t *testing.T) {
	plan := testPlan(t)
	raw, err := InstanceDesiredState(plan, "sg-test123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	var doc map[string]any
	json.Unmarshal(raw, &doc)
	bdm, ok := doc["BlockDeviceMappings"].([]any)
	if !ok || len(bdm) == 0 {
		t.Fatal("BlockDeviceMappings missing")
	}
	ebs := bdm[0].(map[string]any)["Ebs"].(map[string]any)
	if ebs["VolumeSize"] != float64(plan.VolumeSize) {
		t.Errorf("VolumeSize = %v, want %d", ebs["VolumeSize"], plan.VolumeSize)
	}
	if ebs["VolumeType"] != "gp3" {
		t.Errorf("VolumeType = %v, want gp3", ebs["VolumeType"])
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
go test ./internal/workstation/... -run TestSGDesiredState -run TestInstanceDesiredState -v
```

Expected: compile error — `SGDesiredState` and `InstanceDesiredState` undefined.

- [ ] **Step 3: Create `internal/workstation/resources.go`**

```go
package workstation

import "encoding/json"

// SGDesiredState returns the Cloud Control desired-state JSON for the workstation
// security group. Allows TCP 8443 (NICE DCV HTTPS) inbound.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"GroupName":   plan.SGName,
		"Description": "Fabrica-managed security group for cloud workstation (NICE DCV)",
		"VpcId":       plan.VPCID,
		"SecurityGroupIngress": []map[string]any{
			{
				"IpProtocol":  "tcp",
				"FromPort":    plan.DCVPort,
				"ToPort":      plan.DCVPort,
				"CidrIp":      plan.AllowedCIDR,
				"Description": "NICE DCV HTTPS",
			},
		},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.SGName},
		},
	}
	return json.Marshal(doc)
}

// InstanceDesiredState returns the Cloud Control desired-state JSON for the
// workstation EC2 instance.
func InstanceDesiredState(plan *CreatePlan, sgID, userData string) (json.RawMessage, error) {
	doc := map[string]any{
		"ImageId":          plan.AmiID,
		"InstanceType":     plan.InstanceType,
		"SubnetId":         plan.SubnetID,
		"SecurityGroupIds": []string{sgID},
		"UserData":         userData,
		"BlockDeviceMappings": []map[string]any{
			{
				"DeviceName": "/dev/sda1",
				"Ebs": map[string]any{
					"VolumeSize":          plan.VolumeSize,
					"VolumeType":          "gp3",
					"DeleteOnTermination": true,
				},
			},
		},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.InstanceName},
		},
		"MetadataOptions": map[string]any{
			"HttpTokens": "required",
		},
	}
	return json.Marshal(doc)
}
```

- [ ] **Step 4: Run the tests to confirm they pass**

```bash
go test ./internal/workstation/... -v
```

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/workstation/resources.go internal/workstation/resources_test.go
git commit -m "feat: add workstation SGDesiredState and InstanceDesiredState builders"
```

---

## Task 6: `internal/workstation/userdata.go` — cloud-init generator

**Files:**
- Create: `internal/workstation/userdata.go`
- Create: `internal/workstation/userdata_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/workstation/userdata_test.go`:

```go
package workstation

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateRawRequiresSessionPassword(t *testing.T) {
	_, err := GenerateRaw(UserDataConfig{})
	if err == nil {
		t.Fatal("expected error when SessionPassword is empty")
	}
	if !containsStr(err.Error(), "SessionPassword") {
		t.Errorf("error %q should mention SessionPassword", err.Error())
	}
}

func TestGenerateRawContainsDCVInstall(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "hunter2"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	for _, want := range []string{
		"dcv",
		"hunter2",
	} {
		if !containsStr(strings.ToLower(got), strings.ToLower(want)) {
			t.Errorf("userdata does not contain %q", want)
		}
	}
}

func TestGenerateRawIdleTimeout(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{
		SessionPassword:    "pw",
		IdleTimeoutMinutes: 30,
	})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	if !containsStr(got, "30") {
		t.Error("idle timeout 30 should appear in userdata")
	}
}

func TestGenerateProducesValidBase64(t *testing.T) {
	b64, err := Generate(UserDataConfig{SessionPassword: "pw"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if len(decoded) == 0 {
		t.Error("decoded userdata is empty")
	}
}

func TestGenerateRawDefaultIdleTimeout(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "pw"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	// Default is 60 minutes
	if !containsStr(got, "60") {
		t.Error("default idle timeout 60 should appear in userdata")
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
go test ./internal/workstation/... -run TestGenerate -v
```

Expected: compile error — `Generate`, `GenerateRaw`, `UserDataConfig` undefined.

- [ ] **Step 3: Create `internal/workstation/userdata.go`**

```go
package workstation

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"text/template"
)

// UserDataConfig is the input shape for the DCV cloud-init script.
type UserDataConfig struct {
	SessionPassword    string // required; used for the DCV session
	IdleTimeoutMinutes int    // defaults to DefaultIdleTimeoutMinutes
}

var userDataTmpl = template.Must(template.New("userdata").Parse(`#!/bin/bash
set -euo pipefail

# Install NICE DCV server
snap install --classic dcv-server 2>/dev/null || apt-get install -y dcv-server

# Configure NICE DCV
dcv configure-session --type=virtual --storage-root /home/ubuntu/dcv
dcv configure --idle-timeout={{ .IdleTimeoutMinutes }}

# Create a persistent DCV session
dcv create-session --type=virtual --storage-root /home/ubuntu/dcv workstation

# Set DCV session password (non-interactive auth)
echo "dcv:{{ .SessionPassword }}" | chpasswd

systemctl enable dcvsessionmgr dcv-session-manager-agent 2>/dev/null || true
systemctl restart dcvsessionmgr 2>/dev/null || true
`))

// Generate renders the cloud-init script and returns it base64-encoded
// (the format EC2 expects for UserData in Cloud Control).
func Generate(cfg UserDataConfig) (string, error) {
	raw, err := GenerateRaw(cfg)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(raw)), nil
}

// GenerateRaw renders the cloud-init script without base64 encoding.
// Used in tests to inspect script content directly.
func GenerateRaw(cfg UserDataConfig) (string, error) {
	if cfg.SessionPassword == "" {
		return "", fmt.Errorf("SessionPassword must not be empty")
	}
	if cfg.IdleTimeoutMinutes <= 0 {
		cfg.IdleTimeoutMinutes = DefaultIdleTimeoutMinutes
	}
	var buf bytes.Buffer
	if err := userDataTmpl.Execute(&buf, cfg); err != nil {
		return "", fmt.Errorf("rendering userdata template: %w", err)
	}
	return buf.String(), nil
}
```

- [ ] **Step 4: Run the tests to confirm they pass**

```bash
go test ./internal/workstation/... -v
```

Expected: all PASS

- [ ] **Step 5: Run the full build**

```bash
go build ./...
```

Expected: success

- [ ] **Step 6: Commit**

```bash
git add internal/workstation/userdata.go internal/workstation/userdata_test.go
git commit -m "feat: add workstation cloud-init generator"
```

---

## Task 7: `cmd/workstation/create/create.go` — create command

**Files:**
- Create: `cmd/workstation/create/create.go`
- Create: `cmd/workstation/create/create_test.go`
- Create: `cmd/workstation/create/cobra_test.go`

- [ ] **Step 1: Write the failing white-box tests**

Create `cmd/workstation/create/create_test.go`:

```go
package create

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func newTestCommand(out *bytes.Buffer, provider cloud.Provider, st *fabricastate.State) command {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.Workstation.AmiID = "ami-test12345"
	cfg.Workstation.VPCId = "vpc-test"
	cfg.Workstation.SubnetId = "subnet-test"
	c := command{
		runtime: globals.Runtime{
			Config:   cfg,
			Provider: provider,
		},
		costs:   fabricacost.Global,
		out:     out,
		confirm: func(_, _ string) bool { return true },
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.writeState = func(_ *fabricastate.State) error { return nil }
	if provider != nil {
		c.createResource = provider.Resources().Create
	}
	return c
}

func TestCreateDryRunNoAWSCalls(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("dry-run made %d create calls, want 0", provider.createCalls)
	}
}

func TestCreateDryRunOutputFields(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"123456789012",
		"us-east-1",
		"fabrica-workstation-sg",
		"fabrica-workstation",
		"Cost estimate:",
	} {
		assertContains(t, got, want)
	}
}

func TestCreateAlreadyExists(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule(moduleName, "1", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-existing"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-existing"},
	})
	c := newTestCommand(&out, provider, st)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("already-exists: made %d create calls, want 0", provider.createCalls)
	}
	assertContains(t, out.String(), "already provisioned")
}

func TestCreateHappyPathOrderAndState(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	var writtenStates []*fabricastate.State
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
		cp := *s
		writtenStates = append(writtenStates, &cp)
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.createCalls != 2 {
		t.Fatalf("expected 2 create calls, got %d", provider.createCalls)
	}
	if provider.createdTypes[0] != "AWS::EC2::SecurityGroup" {
		t.Errorf("first resource = %q, want AWS::EC2::SecurityGroup", provider.createdTypes[0])
	}
	if provider.createdTypes[1] != "AWS::EC2::Instance" {
		t.Errorf("second resource = %q, want AWS::EC2::Instance", provider.createdTypes[1])
	}
	if len(writtenStates) < 2 {
		t.Fatalf("expected >=2 state writes, got %d", len(writtenStates))
	}
	final := writtenStates[len(writtenStates)-1]
	m := final.GetModule(moduleName)
	if m == nil {
		t.Fatal("workstation module not in final state")
	}
	if len(m.Resources) != 2 {
		t.Fatalf("final state has %d resources, want 2", len(m.Resources))
	}
}

func TestCreateSGFailureNoStateWritten(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{sgCreateErr: errors.New("sg quota")}
	st := fabricastate.NewState("123456789012", "us-east-1")
	stateWritten := false
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = func(_ *fabricastate.State) error {
		stateWritten = true
		return nil
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on SG create failure")
	}
	assertContains(t, err.Error(), "creating security group")
	if stateWritten {
		t.Error("state must not be written when SG creation fails")
	}
}

func TestCreateInstanceFailurePreservesPartialState(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{instanceCreateErr: errors.New("quota exceeded")}
	st := fabricastate.NewState("123456789012", "us-east-1")
	var lastState *fabricastate.State
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
		cp := *s
		lastState = &cp
		return nil
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on instance create failure")
	}
	assertContains(t, err.Error(), "creating EC2 instance")
	if lastState == nil {
		t.Fatal("state was never written")
	}
	_, hasSG := lastState.GetModuleResource(moduleName, "AWS::EC2::SecurityGroup")
	if !hasSG {
		t.Error("SG resource not recorded in state after instance failure")
	}
}

func TestCreateConfirmationRejected(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.confirm = func(_, _ string) bool { return false }

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("cancelled: made %d create calls, want 0", provider.createCalls)
	}
	assertContains(t, out.String(), "Cancelled")
}

func TestCreateNilProviderReturnsError(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: nil},
		costs:   fabricacost.Global,
		out:     &out,
	}
	c.readState = func() (*fabricastate.State, error) { return fabricastate.NewState("", ""), nil }
	c.writeState = func(_ *fabricastate.State) error { return nil }

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	assertContains(t, err.Error(), "no provider configured")
}

func TestCreateIdentityFailure(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{identityErr: errors.New("credentials unavailable")}
	st := fabricastate.NewState("", "")
	c := newTestCommand(&out, provider, st)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when identity fails")
	}
	if provider.createCalls != 0 {
		t.Fatal("identity failure: create was called")
	}
}

func TestCreateInstanceTypeFlagOverridesConfig(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.instanceType = "g5.2xlarge"
	c.runtime.Config.Workstation.InstanceType = "g4dn.xlarge"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "g5.2xlarge")
}

// ---- fakeProvider ----

type fakeProvider struct {
	identityErr       error
	sgCreateErr       error
	instanceCreateErr error
	createCalls       int
	createdTypes      []string
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Identity(_ context.Context) (string, string, string, error) {
	if f.identityErr != nil {
		return "", "", "", f.identityErr
	}
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (f *fakeProvider) Resources() cloud.ResourceClient {
	return &fakeResourceClient{provider: f}
}

type fakeResourceClient struct{ provider *fakeProvider }

func (r *fakeResourceClient) Create(_ context.Context, res *cloud.Resource) error {
	r.provider.createCalls++
	r.provider.createdTypes = append(r.provider.createdTypes, res.TypeName)
	if res.TypeName == "AWS::EC2::SecurityGroup" && r.provider.sgCreateErr != nil {
		return r.provider.sgCreateErr
	}
	if res.TypeName == "AWS::EC2::Instance" && r.provider.instanceCreateErr != nil {
		return r.provider.instanceCreateErr
	}
	switch res.TypeName {
	case "AWS::EC2::SecurityGroup":
		res.Identifier = fmt.Sprintf("sg-fake%04d", r.provider.createCalls)
	case "AWS::EC2::Instance":
		res.Identifier = fmt.Sprintf("i-fake%04d", r.provider.createCalls)
	}
	return nil
}
func (r *fakeResourceClient) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *fakeResourceClient) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *fakeResourceClient) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *fakeResourceClient) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
go test ./cmd/workstation/... -v
```

Expected: compile error — package `create` not found.

- [ ] **Step 3: Create `cmd/workstation/create/create.go`**

```go
package create

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/credentials"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/workstation"
	"github.com/spf13/cobra"
)

const (
	lineWidth   = 58
	moduleName  = "workstation"
	credFile    = ".fabrica/workstation-credentials.yaml"
	passwordLen = 24
)

type command struct {
	runtime      globals.Runtime
	dryRun       bool
	assumeYes    bool
	instanceType string
	volumeSize   int
	out          io.Writer
	costs        *fabricacost.Registry
	confirm      func(string, string) bool

	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	createResource func(ctx context.Context, r *cloud.Resource) error
}

// New returns the "workstation create" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var instanceType string
	var volumeSize int

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Provision a cloud workstation",
		Long: `Provision a NICE DCV cloud workstation on AWS.

Creates two resources in order:
  1. EC2 Security Group — allows TCP 8443 inbound (NICE DCV HTTPS)
  2. EC2 Instance — runs NICE DCV from the provided AMI

State is written after each resource so a partial failure is recoverable:
re-running create will detect the already-provisioned module and exit cleanly.

A random DCV session password is written to .fabrica/workstation-credentials.yaml.

With --dry-run, shows the provisioning plan and a monthly cost estimate without
making any AWS calls.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()

			c := command{
				runtime:      rt,
				dryRun:       opts.DryRun,
				assumeYes:    opts.AssumeYes,
				instanceType: instanceType,
				volumeSize:   volumeSize,
				out:          out,
				costs:        fabricacost.Global,
				confirm:      prompt.ConfirmExact,
			}
			c.readState = c.defaultReadState
			c.writeState = c.defaultWriteState
			if rt.Provider != nil {
				c.createResource = rt.Provider.Resources().Create
			}
			return c.run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(&instanceType, "instance-type", "", "EC2 instance type (default: g4dn.xlarge)")
	cmd.Flags().IntVar(&volumeSize, "volume-size", 0, "EBS root volume size in GiB (default: 100)")
	return cmd
}

func (c command) run(ctx context.Context) error {
	if c.runtime.Provider == nil {
		return fmt.Errorf("no provider configured; run 'fabrica setup' first")
	}

	account, _, region, err := c.runtime.Provider.Identity(ctx)
	if err != nil {
		return fmt.Errorf("resolving identity: %w", err)
	}

	wsCfg := c.runtime.Config.Workstation
	if c.instanceType != "" {
		wsCfg.InstanceType = c.instanceType
	}
	if c.volumeSize > 0 {
		wsCfg.VolumeSize = c.volumeSize
	}

	plan, err := workstation.NewCreatePlan(ctx, wsCfg, account, region, nil)
	if err != nil {
		return fmt.Errorf("building create plan: %w", err)
	}

	if c.dryRun {
		c.printDryRun(plan)
		return nil
	}

	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	if m := st.GetModule(moduleName); m != nil {
		fmt.Fprintf(c.out, "Workstation is already provisioned. Run 'fabrica workstation status' to check health.\n")
		fmt.Fprintf(c.out, "Use 'fabrica workstation terminate' to remove it first.\n")
		return nil
	}

	c.printApplyPlan(plan)

	if !c.assumeYes {
		fmt.Fprintln(c.out)
		phrase := fmt.Sprintf("create workstation %s", account)
		c.printConfirmInstructions(phrase)
		if !c.confirm("Enter confirmation phrase", phrase) {
			fmt.Fprintln(c.out, "Cancelled. No AWS calls were made.")
			return nil
		}
		fmt.Fprintln(c.out, "Confirmation accepted.")
	} else {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Proceeding without interactive confirmation (--yes flag set).")
	}

	return c.applyCreate(ctx, st, plan)
}

func (c command) applyCreate(ctx context.Context, st *fabricastate.State, plan *workstation.CreatePlan) error {
	sessionPass, err := credentials.GeneratePassword(passwordLen)
	if err != nil {
		return fmt.Errorf("generating session password: %w", err)
	}

	credContent := fmt.Sprintf("# Workstation DCV credentials — keep secret\ndcv_session_password: %q\n", sessionPass)
	if err := credentials.WriteCredentials(credFile, credContent); err != nil {
		return fmt.Errorf("writing credentials file: %w", err)
	}
	fmt.Fprintf(c.out, "\nDCV credentials written to %s\n", credFile)

	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Creating security group %s...\n", plan.SGName)

	sgDesired, err := workstation.SGDesiredState(plan)
	if err != nil {
		return fmt.Errorf("building SG desired state: %w", err)
	}
	sg := &cloud.Resource{TypeName: "AWS::EC2::SecurityGroup", DesiredState: sgDesired}
	if err := c.createResource(ctx, sg); err != nil {
		return fmt.Errorf("creating security group: %w", err)
	}
	fmt.Fprintf(c.out, "  Security group created: %s\n", sg.Identifier)

	st.UpsertModule(moduleName, "1", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: sg.Identifier},
	})
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state after SG creation: %w", err)
	}

	fmt.Fprintf(c.out, "Creating instance %s...\n", plan.InstanceName)

	userData, err := workstation.Generate(workstation.UserDataConfig{
		SessionPassword:    sessionPass,
		IdleTimeoutMinutes: plan.IdleTimeoutMinutes,
	})
	if err != nil {
		return fmt.Errorf("generating user data: %w", err)
	}

	instanceDesired, err := workstation.InstanceDesiredState(plan, sg.Identifier, userData)
	if err != nil {
		return fmt.Errorf("building instance desired state: %w", err)
	}
	instance := &cloud.Resource{TypeName: "AWS::EC2::Instance", DesiredState: instanceDesired}
	if err := c.createResource(ctx, instance); err != nil {
		return fmt.Errorf("creating EC2 instance: %w", err)
	}
	fmt.Fprintf(c.out, "  Instance created: %s\n", instance.Identifier)

	st.UpsertModule(moduleName, "1", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: sg.Identifier},
		{TypeName: "AWS::EC2::Instance", Identifier: instance.Identifier},
	})
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state after instance creation: %w", err)
	}

	c.printPostCreate(plan, instance.Identifier)
	return nil
}

func (c command) printDryRun(plan *workstation.CreatePlan) {
	fmt.Fprintln(c.out, "Cloud Workstation (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  AWS account:      %s\n", plan.Account)
	fmt.Fprintf(c.out, "  AWS region:       %s\n", plan.Region)
	fmt.Fprintf(c.out, "  AMI ID:           %s\n", plan.AmiID)
	fmt.Fprintf(c.out, "  Instance type:    %s\n", plan.InstanceType)
	fmt.Fprintf(c.out, "  Volume:           %d GiB gp3\n", plan.VolumeSize)
	fmt.Fprintf(c.out, "  Idle timeout:     %d min\n", plan.IdleTimeoutMinutes)
	if plan.DefaultVPC {
		fmt.Fprintf(c.out, "  VPC:              default (%s)\n", plan.VPCID)
	} else if plan.VPCID != "" {
		fmt.Fprintf(c.out, "  VPC:              %s\n", plan.VPCID)
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to create:")
	fmt.Fprintf(c.out, "  Security Group:   %s\n", plan.SGName)
	fmt.Fprintf(c.out, "  EC2 Instance:     %s\n", plan.InstanceName)
	fmt.Fprintln(c.out)
	c.printCostReport(plan)
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c command) printCostReport(plan *workstation.CreatePlan) {
	report := c.costs.EstimateAll(plan.CostResources)
	fmt.Fprintln(c.out, "Cost estimate:")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  %-30s %10s  %s\n", "Resource", "Cost/mo", "Confidence")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	for _, result := range report.Results {
		if result.Err != nil {
			fmt.Fprintf(c.out, "  %-30s %10s  %s\n", result.Resource.Name, "-", "(no estimate)")
			continue
		}
		fmt.Fprintf(c.out, "  %-30s  $%-8.2f  %s\n", result.Resource.Name, result.Monthly.Amount, result.Monthly.Confidence)
	}
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  %-30s  $%-8.2f\n", "Total:", report.Total)
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "Confidence: %s\n", report.Confidence)
	fmt.Fprintln(c.out)
}

func (c command) printApplyPlan(plan *workstation.CreatePlan) {
	fmt.Fprintln(c.out, "Cloud Workstation")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  AWS account:   %s\n", plan.Account)
	fmt.Fprintf(c.out, "  AWS region:    %s\n", plan.Region)
	fmt.Fprintf(c.out, "  Instance type: %s\n", plan.InstanceType)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to create:")
	fmt.Fprintf(c.out, "  Security Group: %s\n", plan.SGName)
	fmt.Fprintf(c.out, "  EC2 Instance:   %s\n", plan.InstanceName)
}

func (c command) printConfirmInstructions(phrase string) {
	fmt.Fprintln(c.out, "Confirmation required.")
	fmt.Fprintln(c.out, "Type this exact phrase to continue:")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  %s\n", phrase)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Any other input cancels.")
}

func (c command) printPostCreate(plan *workstation.CreatePlan, instanceID string) {
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Cloud Workstation provisioned.")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  Instance ID:   %s\n", instanceID)
	fmt.Fprintf(c.out, "  Status:        provisioning (DCV setup in progress)\n")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  DCV credentials: %s\n", credFile)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  fabrica workstation list     Show workstation details")
}

func (c command) defaultReadState() (*fabricastate.State, error) {
	account, region := "", ""
	if c.runtime.Config != nil {
		account = c.runtime.Config.Cloud.AWS.AccountID
		region = c.runtime.Config.Cloud.AWS.Region
	}
	return fabricastate.ReadStateOrNew(account, region)
}

func (c command) defaultWriteState(st *fabricastate.State) error {
	return fabricastate.WriteState(st)
}
```

- [ ] **Step 4: Run the white-box tests to confirm they pass**

```bash
go test ./cmd/workstation/create/... -run "^Test" -v
```

Expected: all PASS (cobra tests will fail until Step 5)

- [ ] **Step 5: Write cobra_test.go**

Create `cmd/workstation/create/cobra_test.go`:

```go
package create_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/workstation/create"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{
		Use:           "fabrica",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.SetOut(out)
	root.SetErr(out)
	optionsSource := func() globals.Options { return opts }
	root.AddCommand(create.New(runtimeSource, optionsSource, out))
	return root
}

func runCreate(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"create"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newCobraRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.Workstation.AmiID = "ami-test12345"
	cfg.Workstation.VPCId = "vpc-test"
	cfg.Workstation.SubnetId = "subnet-test"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

func TestCreateCobraDryRunNoAWSCalls(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("dry-run made %d create calls, want 0", provider.createCalls)
	}
	assertCobraContains(t, got, "dry run")
}

func TestCreateCobraDryRunOutputFields(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	for _, want := range []string{"123456789012", "us-east-1", "fabrica-workstation-sg", "Cost estimate:"} {
		assertCobraContains(t, got, want)
	}
}

func TestCreateCobraYesFlagSkipsConfirmation(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &cobraFakeProvider{}
	_, err := runCreate(t, newCobraRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("--yes run failed: %v", err)
	}
	if provider.createCalls != 2 {
		t.Fatalf("--yes: expected 2 create calls, got %d", provider.createCalls)
	}
}

func TestCreateCobraNilProvider(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }
	_, err := runCreate(t, runtimeSource)
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	assertCobraContains(t, err.Error(), "no provider configured")
}

func TestCreateCobraInstanceTypeFlag(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run", "--instance-type", "g5.2xlarge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "g5.2xlarge")
}

func TestCreateCobraVolumeSizeFlag(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run", "--volume-size", "200")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "200 GiB")
}

// ---- cobraFakeProvider ----

type cobraFakeProvider struct {
	createCalls int
}

func (f *cobraFakeProvider) Name() string { return "fake" }
func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (f *cobraFakeProvider) Resources() cloud.ResourceClient {
	return &cobraFakeRC{provider: f}
}

type cobraFakeRC struct{ provider *cobraFakeProvider }

func (r *cobraFakeRC) Create(_ context.Context, res *cloud.Resource) error {
	r.provider.createCalls++
	switch res.TypeName {
	case "AWS::EC2::SecurityGroup":
		res.Identifier = fmt.Sprintf("sg-cobra%04d", r.provider.createCalls)
	case "AWS::EC2::Instance":
		res.Identifier = fmt.Sprintf("i-cobra%04d", r.provider.createCalls)
	}
	return nil
}
func (r *cobraFakeRC) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *cobraFakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}

func assertCobraContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}
```

- [ ] **Step 6: Run all create tests**

```bash
go test ./cmd/workstation/create/... -v
```

Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/workstation/create/create.go cmd/workstation/create/create_test.go cmd/workstation/create/cobra_test.go
git commit -m "feat: add workstation create command"
```

---

## Task 8: `cmd/workstation/list/list.go` — list command

**Files:**
- Create: `cmd/workstation/list/list.go`
- Create: `cmd/workstation/list/list_test.go`
- Create: `cmd/workstation/list/cobra_test.go`

- [ ] **Step 1: Write the failing white-box tests**

Create `cmd/workstation/list/list_test.go`:

```go
package list

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func newTestCommand(out *bytes.Buffer, st *fabricastate.State) command {
	cfg := config.Defaults()
	c := command{
		runtime: globals.Runtime{Config: cfg},
		out:     out,
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	return c
}

func TestListNoneProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "no workstations")
}

func TestListShowsProvisionedWorkstation(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule(moduleName, "1", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc123"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc123"},
	})
	c := newTestCommand(&out, st)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assertContains(t, got, "i-abc123")
	assertContains(t, got, "provisioning")
}

func TestListJSONNoneProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st)
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assertContains(t, got, `"workstations"`)
	assertContains(t, got, `[]`)
}

func TestListJSONShowsWorkstation(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule(moduleName, "1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-xyz"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-xyz"},
	})
	c := newTestCommand(&out, st)
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assertContains(t, got, "i-xyz")
	assertContains(t, got, "ready")
}

func TestListReadStateError(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	c := command{
		runtime: globals.Runtime{Config: cfg},
		out:     &out,
	}
	c.readState = func() (*fabricastate.State, error) {
		return nil, errors.New("disk read failure")
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
	assertContains(t, err.Error(), "reading state")
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
go test ./cmd/workstation/list/... -v
```

Expected: compile error — package `list` not found.

- [ ] **Step 3: Create `cmd/workstation/list/list.go`**

```go
package list

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "workstation"
)

// WorkstationEntry is the JSON-serialisable view of one workstation.
type WorkstationEntry struct {
	Status     string `json:"status"`
	InstanceID string `json:"instanceId,omitempty"`
	SGID       string `json:"sgId,omitempty"`
}

// ListOutput is the JSON-serialisable result of a list run.
type ListOutput struct {
	Workstations []WorkstationEntry `json:"workstations"`
}

type command struct {
	runtime globals.Runtime
	jsonOut bool
	out     io.Writer

	readState func() (*fabricastate.State, error)
}

// New returns the "workstation list" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List provisioned workstations",
		Long:  `List all workstations tracked in local Fabrica state.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime: rt,
				jsonOut: opts.JSONOutput,
				out:     out,
			}
			c.readState = c.defaultReadState
			return c.run(cmd.Context())
		},
	}
	return cmd
}

func (c command) run(_ context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(moduleName)

	if c.jsonOut {
		c.printJSON(m)
		return nil
	}

	c.printText(m)
	return nil
}

func (c command) printText(m *fabricastate.ModuleState) {
	if m == nil {
		fmt.Fprintln(c.out, "No workstations provisioned. Run 'fabrica workstation create' to provision one.")
		return
	}
	fmt.Fprintln(c.out, "Workstations")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Status: %s\n", m.Status)

	if r, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance"); ok {
		fmt.Fprintf(c.out, "  Instance ID: %s\n", r.Identifier)
	}
	if r, ok := stateutil.ResourceByType(m, "AWS::EC2::SecurityGroup"); ok {
		fmt.Fprintf(c.out, "  Security Group: %s\n", r.Identifier)
	}
}

func (c command) printJSON(m *fabricastate.ModuleState) {
	out := ListOutput{Workstations: []WorkstationEntry{}}
	if m != nil {
		entry := WorkstationEntry{Status: m.Status}
		if r, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance"); ok {
			entry.InstanceID = r.Identifier
		}
		if r, ok := stateutil.ResourceByType(m, "AWS::EC2::SecurityGroup"); ok {
			entry.SGID = r.Identifier
		}
		out.Workstations = append(out.Workstations, entry)
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(c.out, string(data))
}

func (c command) defaultReadState() (*fabricastate.State, error) {
	account, region := "", ""
	if c.runtime.Config != nil {
		account = c.runtime.Config.Cloud.AWS.AccountID
		region = c.runtime.Config.Cloud.AWS.Region
	}
	return fabricastate.ReadStateOrNew(account, region)
}
```

- [ ] **Step 4: Run the white-box tests**

```bash
go test ./cmd/workstation/list/... -run "^Test" -v
```

Expected: all PASS (cobra tests fail until Step 5)

- [ ] **Step 5: Write cobra_test.go**

Create `cmd/workstation/list/cobra_test.go`:

```go
package list_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/workstation/list"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{
		Use:           "fabrica",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.SetOut(out)
	root.SetErr(out)
	optionsSource := func() globals.Options { return opts }
	root.AddCommand(list.New(runtimeSource, optionsSource, out))
	return root
}

func runList(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"list"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newCobraRuntime() globals.RuntimeSource {
	t := &testing.T{}
	_ = t
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	return func() (globals.Runtime, error) { return rt, nil }
}

func TestListCobraNoneProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runList(t, newCobraRuntime())
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	assertCobraContains(t, got, "no workstations")
}

func TestListCobraJSONNoneProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runList(t, newCobraRuntime(), "--json")
	if err != nil {
		t.Fatalf("list --json failed: %v", err)
	}
	assertCobraContains(t, got, `"workstations"`)
}

func assertCobraContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}
```

- [ ] **Step 6: Run all list tests**

```bash
go test ./cmd/workstation/list/... -v
```

Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/workstation/list/list.go cmd/workstation/list/list_test.go cmd/workstation/list/cobra_test.go
git commit -m "feat: add workstation list command"
```

---

## Task 9: Wire everything together

**Files:**
- Create: `cmd/workstation/workstation.go`
- Modify: `cmd/root/root.go`

- [ ] **Step 1: Create `cmd/workstation/workstation.go`**

```go
package workstation

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/workstation/create"
	"github.com/jpvelasco/fabrica/cmd/workstation/list"
	"github.com/spf13/cobra"
)

// New returns the "workstation" parent command with create and list subcommands.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workstation",
		Short: "Manage cloud workstations",
		Long: `Manage NICE DCV cloud workstations on AWS.

Available operations:
  create     Provision a new cloud workstation on EC2
  list       Show provisioned workstations`,
	}
	cmd.AddCommand(create.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(list.New(runtimeSource, optionsSource, out))
	return cmd
}
```

- [ ] **Step 2: Register workstation in `cmd/root/root.go`**

Add the import (after the horde import):

```go
"github.com/jpvelasco/fabrica/cmd/workstation"
```

Add the command registration (after the horde line):

```go
cmd.AddCommand(workstation.New(runtimeSource, optionsSource, out))
```

- [ ] **Step 3: Build the entire project**

```bash
go build ./...
```

Expected: success

- [ ] **Step 4: Run all tests**

```bash
go test ./...
```

Expected: all PASS

- [ ] **Step 5: Run lint**

```bash
golangci-lint run ./...
```

Expected: 0 issues. Fix any reported issues before committing.

- [ ] **Step 6: Commit**

```bash
git add cmd/workstation/workstation.go cmd/root/root.go
git commit -m "feat: wire workstation command into root"
```

---

## Task 10: Update AGENTS.md

**Files:**
- Modify: `AGENTS.md`

- [ ] **Step 1: Add workstation to the module table**

In `AGENTS.md`, find the "Current Modules" table and add a row:

```markdown
| `workstation` | `create`, `list` | Provisions a NICE DCV cloud workstation on EC2 (AMI-first, g4dn.xlarge default); probes port 8443; writes DCV session credentials to `.fabrica/workstation-credentials.yaml` |
```

- [ ] **Step 2: Build and test to confirm no regressions**

```bash
go build ./... && go test ./...
```

Expected: all PASS

- [ ] **Step 3: Commit**

```bash
git add AGENTS.md
git commit -m "docs: add workstation module to AGENTS.md"
```

---

## Task 11: Final verification

- [ ] **Step 1: Run full test suite with verbose output**

```bash
go test ./... -v 2>&1 | tail -30
```

Expected: all PASS, no failures.

- [ ] **Step 2: Run lint**

```bash
golangci-lint run ./...
```

Expected: 0 issues.

- [ ] **Step 3: Format check**

```bash
gofmt -l .
```

Expected: no output (all files already formatted).

- [ ] **Step 4: Layering check**

```bash
go list -deps ./internal/cloud/...
```

Expected: output does NOT contain `internal/state`, `internal/cost`, or `cmd/`.

- [ ] **Step 5: Smoke-test the CLI**

```bash
go run . workstation --help
go run . workstation create --help
go run . workstation list --help
```

Expected: usage text printed for each command, no errors.

- [ ] **Step 6: Smoke-test dry-run (requires a fabrica.yaml with workstation.amiId)**

Create a minimal test config:

```bash
cat > /tmp/fabrica-ws-test.yaml << 'EOF'
cloud:
  provider: aws
  aws:
    region: us-east-1
workstation:
  amiId: ami-test12345
  vpcId: vpc-test
  subnetId: subnet-test
EOF
go run . --config /tmp/fabrica-ws-test.yaml workstation create --dry-run
```

Expected: dry-run output with resources, cost estimate table, no AWS calls.

---

## Self-Review

### Spec coverage check

| Spec requirement | Covered by |
|---|---|
| `fabrica workstation create` end-to-end | Task 7 |
| All tests pass (white-box + black-box) | Tasks 4–8 |
| Lint clean | Task 11 |
| AGENTS.md updated | Task 10 |
| Code matches perforce/horde template | All tasks |
| `fabrica workstation list` | Task 8 |
| Cost estimate with GPU types | Task 2 |
| NICE DCV AMI-first (amiId required) | Task 4 |
| Idle timeout configurable | Tasks 4, 6 |
| DCV credentials written to .fabrica/ | Task 7 |
| Config in internal/config/config.go | Task 1 |
| Wired into root | Task 9 |

### Placeholder scan

None found. All steps contain working code.

### Type consistency check

- `WorkstationConfig` defined in Task 1, used in Tasks 4, 7
- `CreatePlan` defined in Task 4, used in Tasks 5, 6, 7
- `UserDataConfig` defined in Task 6, used in Task 7
- `SGDesiredState(plan *CreatePlan)` defined in Task 5, called in Task 7
- `InstanceDesiredState(plan *CreatePlan, sgID, userData string)` defined in Task 5, called in Task 7
- `Generate(cfg UserDataConfig)` defined in Task 6, called in Task 7
- `moduleName = "workstation"` consistent across Task 7 (create) and Task 8 (list)
- `VolumeSize` dry-run output: Task 7 prints `"%d GiB gp3"` → cobra test checks `"200 GiB"` ✓
