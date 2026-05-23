# Horde V1 — `fabrica horde create` Implementation Plan

## Files

| Action | Path |
|---|---|
| Create | `internal/horde/config.go` |
| Create | `internal/horde/plan.go` |
| Create | `internal/horde/plan_test.go` |
| Create | `internal/horde/resources.go` |
| Create | `internal/horde/resources_test.go` |
| Create | `internal/horde/userdata.go` |
| Create | `internal/horde/userdata_test.go` |
| Create | `internal/horde/cost.go` |
| Create | `internal/horde/cost_test.go` |
| Modify | `internal/config/config.go` |
| Create | `cmd/horde/horde.go` |
| Create | `cmd/horde/create/create.go` |
| Create | `cmd/horde/create/create_test.go` |
| Create | `cmd/horde/create/cobra_test.go` |
| Modify | `cmd/root/root.go` |

---

## Task 1: `internal/horde/config.go` + `plan.go`

### Step 1: Write failing tests for `NewCreatePlan`

```go
// internal/horde/plan_test.go
package horde

import (
    "context"
    "testing"

    "github.com/jpvelasco/fabrica/internal/config"
)

func TestNewCreatePlanMissingAmiID(t *testing.T) {
    cfg := config.HordeConfig{}
    _, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
    if err == nil {
        t.Fatal("expected error when AmiID is empty")
    }
    assertContains(t, err.Error(), "horde.amiId is required")
    assertContains(t, err.Error(), "docs/horde-ami.md")
}

func TestNewCreatePlanDefaults(t *testing.T) {
    cfg := config.HordeConfig{AmiID: "ami-abc123"}
    plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if plan.InstanceType != "m7i.xlarge" {
        t.Errorf("InstanceType = %q, want m7i.xlarge", plan.InstanceType)
    }
    if plan.VolumeSize != 100 {
        t.Errorf("VolumeSize = %d, want 100", plan.VolumeSize)
    }
    if plan.Port != 5000 {
        t.Errorf("Port = %d, want 5000", plan.Port)
    }
    if plan.GRPCPort != 5002 {
        t.Errorf("GRPCPort = %d, want 5002", plan.GRPCPort)
    }
    if plan.AllowedCIDR != "10.0.0.0/8" {
        t.Errorf("AllowedCIDR = %q, want 10.0.0.0/8", plan.AllowedCIDR)
    }
    if plan.SGName != "fabrica-horde-sg" {
        t.Errorf("SGName = %q, want fabrica-horde-sg", plan.SGName)
    }
    if plan.InstanceName != "fabrica-horde" {
        t.Errorf("InstanceName = %q, want fabrica-horde", plan.InstanceName)
    }
}

func TestNewCreatePlanVPCResolver(t *testing.T) {
    cfg := config.HordeConfig{AmiID: "ami-abc123"}
    resolver := &fakeVPCResolver{vpcID: "vpc-fake", subnetID: "subnet-fake"}
    plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", resolver)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if plan.VPCID != "vpc-fake" {
        t.Errorf("VPCID = %q, want vpc-fake", plan.VPCID)
    }
    if !plan.DefaultVPC {
        t.Error("DefaultVPC should be true when resolver was used")
    }
}

func TestNewCreatePlanExplicitVPC(t *testing.T) {
    cfg := config.HordeConfig{
        AmiID:    "ami-abc123",
        VPCId:    "vpc-explicit",
        SubnetId: "subnet-explicit",
    }
    plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if plan.DefaultVPC {
        t.Error("DefaultVPC should be false when VPC is explicit")
    }
}

func TestNewCreatePlanCostResources(t *testing.T) {
    cfg := config.HordeConfig{AmiID: "ami-abc123"}
    plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-east-1", nil)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(plan.CostResources) != 2 {
        t.Fatalf("CostResources len = %d, want 2", len(plan.CostResources))
    }
}

type fakeVPCResolver struct{ vpcID, subnetID string }

func (f *fakeVPCResolver) ResolveDefaultVPC(_ context.Context) (string, string, error) {
    return f.vpcID, f.subnetID, nil
}

func assertContains(t *testing.T, s, sub string) {
    t.Helper()
    for i := 0; i <= len(s)-len(sub); i++ {
        if s[i:i+len(sub)] == sub {
            return
        }
    }
    t.Fatalf("%q does not contain %q", s, sub)
}
```

Run: `go test ./internal/horde/... -run TestNewCreatePlan`
Expected: FAIL (package doesn't exist yet)

### Step 2: Create `internal/horde/config.go`

```go
package horde

import "context"

// VPCResolver resolves VPC and subnet IDs without requiring AWS SDK imports here.
type VPCResolver interface {
    ResolveDefaultVPC(ctx context.Context) (vpcID, subnetID string, err error)
}
```

### Step 3: Add `HordeConfig` to `internal/config/config.go`

In `internal/config/config.go`, add the struct and update the `Config` type:

```go
// HordeConfig holds the horde: section of fabrica.yaml.
type HordeConfig struct {
    AmiID        string `mapstructure:"amiId"        yaml:"amiId"`
    InstanceType string `mapstructure:"instanceType" yaml:"instanceType"`
    VolumeSize   int    `mapstructure:"volumeSize"   yaml:"volumeSize"`
    VPCId        string `mapstructure:"vpcId"        yaml:"vpcId"`
    SubnetId     string `mapstructure:"subnetId"     yaml:"subnetId"`
    Port         int    `mapstructure:"port"         yaml:"port"`
    GRPCPort     int    `mapstructure:"grpcPort"     yaml:"grpcPort"`
    AllowedCIDR  string `mapstructure:"allowedCidr"  yaml:"allowedCidr"`
}
```

Change `Config.Horde` from `any` to `HordeConfig`, and update `fileConfig` struct + `fileConfig()` method the same way. Remove the `emptySection(c.Horde)` call — `HordeConfig` serializes cleanly as a struct.

### Step 4: Create `internal/horde/plan.go`

```go
package horde

import (
    "context"
    "fmt"

    "github.com/jpvelasco/fabrica/internal/config"
    "github.com/jpvelasco/fabrica/internal/cost"
)

const (
    TypeAWSEC2Instance = "AWS::EC2::Instance"
    TypeAWSEC2Volume   = "AWS::EC2::Volume"
)

type CreatePlan struct {
    Account      string
    Region       string
    AmiID        string
    InstanceType string
    VolumeSize   int
    Port         int
    GRPCPort     int
    AllowedCIDR  string
    VPCID        string
    SubnetID     string
    DefaultVPC   bool
    SGName       string
    InstanceName string
    CostResources []cost.Resource
}

func NewCreatePlan(ctx context.Context, cfg config.HordeConfig, account, region string, resolver VPCResolver) (*CreatePlan, error) {
    if cfg.AmiID == "" {
        return nil, fmt.Errorf("horde.amiId is required. Provide an AMI ID that contains MongoDB, Redis,\nand the Horde server. See: https://github.com/jpvelasco/fabrica/blob/main/docs/horde-ami.md")
    }

    instanceType := cfg.InstanceType
    if instanceType == "" {
        instanceType = "m7i.xlarge"
    }
    volumeSize := cfg.VolumeSize
    if volumeSize <= 0 {
        volumeSize = 100
    }
    port := cfg.Port
    if port <= 0 {
        port = 5000
    }
    grpcPort := cfg.GRPCPort
    if grpcPort <= 0 {
        grpcPort = 5002
    }
    allowedCIDR := cfg.AllowedCIDR
    if allowedCIDR == "" {
        allowedCIDR = "10.0.0.0/8"
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
        Account:      account,
        Region:       region,
        AmiID:        cfg.AmiID,
        InstanceType: instanceType,
        VolumeSize:   volumeSize,
        Port:         port,
        GRPCPort:     grpcPort,
        AllowedCIDR:  allowedCIDR,
        VPCID:        vpcID,
        SubnetID:     subnetID,
        DefaultVPC:   defaultVPC,
        SGName:       "fabrica-horde-sg",
        InstanceName: "fabrica-horde",
        CostResources: []cost.Resource{
            {TypeName: TypeAWSEC2Instance, Name: instanceType},
            {TypeName: TypeAWSEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
        },
    }, nil
}
```

### Step 5: Run tests and verify they pass

Run: `go test ./internal/horde/... -run TestNewCreatePlan`
Expected: PASS

### Step 6: Commit

```bash
git add internal/horde/config.go internal/horde/plan.go internal/horde/plan_test.go internal/config/config.go
git commit -m "feat: add internal/horde config, plan layer, and HordeConfig"
```

---

## Task 2: `internal/horde/resources.go`

### Step 1: Write failing test

```go
// internal/horde/resources_test.go
package horde

import (
    "encoding/json"
    "testing"
)

func TestSGDesiredStateShape(t *testing.T) {
    plan := &CreatePlan{
        SGName:      "fabrica-horde-sg",
        VPCID:       "vpc-abc123",
        Port:        5000,
        GRPCPort:    5002,
        AllowedCIDR: "10.0.0.0/8",
    }
    raw, err := SGDesiredState(plan)
    if err != nil {
        t.Fatalf("SGDesiredState: %v", err)
    }
    var doc map[string]any
    if err := json.Unmarshal(raw, &doc); err != nil {
        t.Fatalf("invalid JSON: %v", err)
    }
    if doc["GroupName"] != "fabrica-horde-sg" {
        t.Errorf("GroupName = %v, want fabrica-horde-sg", doc["GroupName"])
    }
    ingress, ok := doc["SecurityGroupIngress"].([]any)
    if !ok || len(ingress) != 2 {
        t.Fatalf("SecurityGroupIngress len = %d, want 2", len(ingress))
    }
    // Verify both ports use AllowedCIDR
    for _, rule := range ingress {
        r := rule.(map[string]any)
        if r["CidrIp"] != "10.0.0.0/8" {
            t.Errorf("CidrIp = %v, want 10.0.0.0/8", r["CidrIp"])
        }
    }
}

func TestInstanceDesiredStateShape(t *testing.T) {
    plan := &CreatePlan{
        InstanceName: "fabrica-horde",
        InstanceType: "m7i.xlarge",
        AmiID:        "ami-abc123",
        SubnetID:     "subnet-abc",
        VolumeSize:   100,
    }
    raw, err := InstanceDesiredState(plan, "sg-abc123", "dXNlcmRhdGE=")
    if err != nil {
        t.Fatalf("InstanceDesiredState: %v", err)
    }
    var doc map[string]any
    if err := json.Unmarshal(raw, &doc); err != nil {
        t.Fatalf("invalid JSON: %v", err)
    }
    if doc["ImageId"] != "ami-abc123" {
        t.Errorf("ImageId = %v, want ami-abc123", doc["ImageId"])
    }
    if doc["InstanceType"] != "m7i.xlarge" {
        t.Errorf("InstanceType = %v, want m7i.xlarge", doc["InstanceType"])
    }
    meta, ok := doc["MetadataOptions"].(map[string]any)
    if !ok || meta["HttpTokens"] != "required" {
        t.Error("IMDSv2 not enforced")
    }
}
```

### Step 2: Create `internal/horde/resources.go`

```go
package horde

import "encoding/json"

// SGDesiredState returns the Cloud Control desired-state JSON for the Horde
// security group. Opens ports 5000 (HTTP) and 5002 (gRPC) to AllowedCIDR.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
    doc := map[string]any{
        "GroupName":   plan.SGName,
        "Description": "Fabrica-managed security group for Horde coordinator",
        "VpcId":       plan.VPCID,
        "SecurityGroupIngress": []map[string]any{
            {
                "IpProtocol":  "tcp",
                "FromPort":    plan.Port,
                "ToPort":      plan.Port,
                "CidrIp":      plan.AllowedCIDR,
                "Description": "Horde HTTP API + web UI",
            },
            {
                "IpProtocol":  "tcp",
                "FromPort":    plan.GRPCPort,
                "ToPort":      plan.GRPCPort,
                "CidrIp":      plan.AllowedCIDR,
                "Description": "Horde gRPC (agent connections)",
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
// Horde EC2 instance. ImageId is the user-provided AMI ID from HordeConfig.
func InstanceDesiredState(plan *CreatePlan, sgID, userData string) (json.RawMessage, error) {
    doc := map[string]any{
        "ImageId":          plan.AmiID,
        "InstanceType":     plan.InstanceType,
        "SubnetId":         plan.SubnetID,
        "SecurityGroupIds": []string{sgID},
        "UserData":         userData,
        "BlockDeviceMappings": []map[string]any{
            {
                "DeviceName": "/dev/sdf",
                "Ebs": map[string]any{
                    "VolumeSize":          plan.VolumeSize,
                    "VolumeType":          "gp3",
                    "DeleteOnTermination": false,
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

### Step 3: Run tests and commit

Run: `go test ./internal/horde/... -run TestSGDesiredState -run TestInstanceDesiredState`
Expected: PASS

```bash
git add internal/horde/resources.go internal/horde/resources_test.go
git commit -m "feat: add horde SGDesiredState and InstanceDesiredState"
```

---

## Task 3: `internal/horde/userdata.go`

### Step 1: Write failing tests

```go
// internal/horde/userdata_test.go
package horde

import (
    "encoding/base64"
    "strings"
    "testing"
)

func TestGenerateRawContainsPassword(t *testing.T) {
    cfg := UserDataConfig{MongoPassword: "testpass123", Port: 5000, GRPCPort: 5002}
    got, err := GenerateRaw(cfg)
    if err != nil {
        t.Fatalf("GenerateRaw: %v", err)
    }
    if !strings.Contains(got, "testpass123") {
        t.Error("password not found in rendered script")
    }
}

func TestGenerateRawContainsPipefail(t *testing.T) {
    cfg := UserDataConfig{MongoPassword: "p", Port: 5000, GRPCPort: 5002}
    got, err := GenerateRaw(cfg)
    if err != nil {
        t.Fatalf("GenerateRaw: %v", err)
    }
    if !strings.Contains(got, "set -euo pipefail") {
        t.Error("set -euo pipefail not found")
    }
}

func TestGenerateRawEmptyPasswordErrors(t *testing.T) {
    _, err := GenerateRaw(UserDataConfig{Port: 5000, GRPCPort: 5002})
    if err == nil {
        t.Fatal("expected error for empty MongoPassword")
    }
}

func TestGenerateRawContainsHordeReady(t *testing.T) {
    cfg := UserDataConfig{MongoPassword: "p", Port: 5000, GRPCPort: 5002}
    got, err := GenerateRaw(cfg)
    if err != nil {
        t.Fatalf("GenerateRaw: %v", err)
    }
    if !strings.Contains(got, "horde-ready") {
        t.Error("readiness sentinel not found in script")
    }
}

func TestGenerateReturnsBase64(t *testing.T) {
    cfg := UserDataConfig{MongoPassword: "p", Port: 5000, GRPCPort: 5002}
    got, err := Generate(cfg)
    if err != nil {
        t.Fatalf("Generate: %v", err)
    }
    decoded, err := base64.StdEncoding.DecodeString(got)
    if err != nil {
        t.Fatalf("output is not valid base64: %v", err)
    }
    if !strings.Contains(string(decoded), "#!/bin/bash") {
        t.Error("decoded output does not start with #!/bin/bash")
    }
}
```

### Step 2: Create `internal/horde/userdata.go`

```go
package horde

import (
    "bytes"
    "encoding/base64"
    "fmt"
    "text/template"
)

// UserDataConfig is the input shape for the Horde cloud-init script.
type UserDataConfig struct {
    MongoPassword string
    Port          int
    GRPCPort      int
}

var userDataTmpl = template.Must(template.New("horde-userdata").Parse(`#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/fabrica-horde-init.log) 2>&1

# Wait for MongoDB to be healthy (may be starting from AMI service)
for i in $(seq 1 12); do
  mongosh --eval "db.adminCommand('ping')" --quiet && break
  [ $i -eq 12 ] && echo "ERROR: MongoDB did not become healthy within 60s" && exit 1
  sleep 5
done

# Create horde database user (idempotent)
mongosh admin --eval "
  if (!db.getUser('horde')) {
    db.createUser({
      user: 'horde',
      pwd: '{{ .MongoPassword }}',
      roles: [{ role: 'readWrite', db: 'Horde' }]
    });
  }
"

# Write Horde Server.json
mkdir -p /etc/horde
cat > /etc/horde/Server.json <<'HORDEEOF'
{
  "Horde": {
    "DatabaseConnectionString": "mongodb://horde:{{ .MongoPassword }}@localhost:27017/Horde?authSource=admin&readPreference=primary",
    "RedisConnectionConfig": "127.0.0.1:6379",
    "HttpPort": {{ .Port }},
    "Http2Port": {{ .GRPCPort }}
  }
}
HORDEEOF

# Start services in dependency order
systemctl restart redis-server || systemctl restart redis
systemctl restart horde

touch /var/lib/cloud/instance/horde-ready
`))

// GenerateRaw renders the cloud-init script without base64 encoding.
// Used in tests to inspect script content directly.
func GenerateRaw(cfg UserDataConfig) (string, error) {
    if cfg.MongoPassword == "" {
        return "", fmt.Errorf("MongoPassword must not be empty")
    }
    var buf bytes.Buffer
    if err := userDataTmpl.Execute(&buf, cfg); err != nil {
        return "", fmt.Errorf("rendering userdata template: %w", err)
    }
    return buf.String(), nil
}

// Generate renders the cloud-init script and returns it base64-encoded
// (the format EC2 expects for UserData in Cloud Control).
func Generate(cfg UserDataConfig) (string, error) {
    raw, err := GenerateRaw(cfg)
    if err != nil {
        return "", err
    }
    return base64.StdEncoding.EncodeToString([]byte(raw)), nil
}
```

### Step 3: Run tests and commit

Run: `go test ./internal/horde/... -run TestGenerate`
Expected: PASS

```bash
git add internal/horde/userdata.go internal/horde/userdata_test.go
git commit -m "feat: add horde cloud-init userdata generator"
```

---

## Task 4: `internal/horde/cost.go`

### Step 1: Write failing tests

```go
// internal/horde/cost_test.go
package horde

import (
    "testing"

    "github.com/jpvelasco/fabrica/internal/cost"
)

func TestM7iXlargeEstimate(t *testing.T) {
    m, err := cost.Global.Estimate(TypeAWSEC2Instance, cost.Resource{TypeName: TypeAWSEC2Instance, Name: "m7i.xlarge"})
    if err != nil {
        t.Fatalf("estimate: %v", err)
    }
    // $0.2016/hr * 730hr = $147.17
    if m.Amount < 147.0 || m.Amount > 148.0 {
        t.Errorf("m7i.xlarge monthly = %.2f, want ~147.17", m.Amount)
    }
    if m.Confidence != cost.High {
        t.Errorf("confidence = %v, want High", m.Confidence)
    }
}

func TestM7i2xlargeEstimate(t *testing.T) {
    m, err := cost.Global.Estimate(TypeAWSEC2Instance, cost.Resource{TypeName: TypeAWSEC2Instance, Name: "m7i.2xlarge"})
    if err != nil {
        t.Fatalf("estimate: %v", err)
    }
    if m.Amount < 294.0 || m.Amount > 295.0 {
        t.Errorf("m7i.2xlarge monthly = %.2f, want ~294.34", m.Amount)
    }
}

func TestGP3VolumeEstimate100GiB(t *testing.T) {
    m, err := cost.Global.Estimate(TypeAWSEC2Volume, cost.Resource{TypeName: TypeAWSEC2Volume, Name: "gp3-100GiB"})
    if err != nil {
        t.Fatalf("estimate: %v", err)
    }
    // $0.08/GiB * 100 = $8.00
    if m.Amount != 8.0 {
        t.Errorf("gp3-100GiB monthly = %.2f, want 8.00", m.Amount)
    }
}
```

**Note:** `TypeAWSEC2Instance` and `TypeAWSEC2Volume` are already defined in `plan.go`. The cost registry uses the same TypeName keys as the perforce module, but the estimator implementations are different (m7i prices vs m5 prices). Because `cost.Global.Register` panics on duplicate keys, the horde `cost.go` must register under the **same** TypeNames. This means `internal/horde/cost.go` should NOT register `AWS::EC2::Instance` and `AWS::EC2::Volume` separately — it will panic since `internal/perforce/cost.go` already registered them.

**Resolution:** The horde cost estimator must handle m7i prices within the existing `ec2InstanceEstimator`. The plan is to **extend** `internal/perforce/cost.go` to include m7i prices, rather than creating a conflicting registration. Alternatively, factor the shared estimator to `internal/cost/estimators_ec2.go`.

**Recommended approach:** Add m7i prices to `internal/perforce/cost.go`'s `ec2InstancePrices` map. No separate `internal/horde/cost.go` needed — just extend the existing table. Update the test file to live in `internal/perforce/`.

```go
// Add to ec2InstancePrices in internal/perforce/cost.go:
"m7i.xlarge":  0.2016,
"m7i.2xlarge": 0.4032,
"m7i.4xlarge": 0.8064,
"m7i.8xlarge": 1.6128,
```

`internal/horde/cost.go` is then **not needed** as a separate file. The `CostResources` in `CreatePlan` use `TypeAWSEC2Instance` and `TypeAWSEC2Volume` which resolve through the already-registered perforce estimators (now extended with m7i).

### Step 2: Extend `internal/perforce/cost.go`

Add m7i entries to `ec2InstancePrices`. Run: `go test ./internal/perforce/... -run TestM7i`
Expected: PASS (after extending the map, the cost_test assertions pass).

### Step 3: Verify horde plan cost resources resolve

Run: `go test ./internal/horde/... -run TestNewCreatePlanCostResources`

### Step 4: Commit

```bash
git add internal/perforce/cost.go
git commit -m "feat: add m7i instance prices to EC2 cost estimator"
```

---

## Task 5: `cmd/horde/create/create.go`

### Step 1: Write failing white-box tests

Key tests to write in `cmd/horde/create/create_test.go`:

```go
package create

import (
    "bytes"
    "context"
    "errors"
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
    cfg.Horde.AmiID = "ami-test123"
    c := command{
        runtime: globals.Runtime{Config: cfg, Provider: provider},
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

func TestCreateDryRunNoAWSCalls(t *testing.T) { /* mirrors perforce pattern */ }
func TestCreateDryRunOutputFields(t *testing.T) { /* check account, region, sg name, instance name, cost */ }
func TestCreateAlreadyProvisioned(t *testing.T) { /* check "already provisioned" message */ }
func TestCreateMissingAmiID(t *testing.T) { /* cfg.Horde.AmiID = "" → error with horde-ami.md link */ }
func TestCreateHappyPathOrderAndState(t *testing.T) { /* SG before instance, 2 state writes */ }
func TestCreateInstanceFailurePreservesPartialState(t *testing.T) { /* SG in state on instance error */ }
func TestCreateConfirmationRejected(t *testing.T) { /* confirm returns false → 0 create calls */ }
func TestCreateNilProvider(t *testing.T) { /* "No infrastructure configured" */ }
func TestCreateAllowedCIDRWarning(t *testing.T) { /* cfg.Horde.AllowedCIDR = "0.0.0.0/0" → warning in output */ }
func TestCreateDryRunDefaultVPCNote(t *testing.T) { /* DefaultVPC=true → "Default VPC used" note */ }
func TestCreateDryRunM7i2xlargeRecommendation(t *testing.T) { /* dry-run mentions m7i.2xlarge for >10 agents */ }
```

(Full test body follows the same pattern as `cmd/perforce/create/create_test.go`. See that file as the canonical reference.)

### Step 2: Create `cmd/horde/create/create.go`

Key structure (mirrors `cmd/perforce/create/create.go`):

```go
package create

const (
    lineWidth   = 58
    moduleName  = "horde"
    credFile    = ".fabrica/horde-credentials.yaml"
    passwordLen = 24
    passwordChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
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
```

`New()` accepts `RuntimeSource` + `OptionsSource`. Flags: `--instance-type`, `--volume-size`. No `--version` flag (AMI-first; no version Fabrica manages).

`run()` flow:
1. Guard nil provider
2. Call `provider.Identity()` for account/region
3. Apply flag overrides to `cfg.Horde`
4. Call `horde.NewCreatePlan()` — returns error if AmiID empty
5. Dry-run branch: `printDryRun(plan)` and return
6. Read state, check for existing `"horde"` module
7. Print apply plan + confirm phrase `"create horde <account>"`
8. Call `applyCreate()`

`applyCreate()` flow:
1. Generate MongoDB password (24 chars, alphanumeric)
2. Write `.fabrica/horde-credentials.yaml` (mode 0600)
3. Create SG via `createResource` → write state
4. Generate cloud-init via `horde.Generate(UserDataConfig{...})`
5. Create instance via `createResource` → write state
6. Print post-create output

**Post-create output** (from spec):
```
Horde coordinator provisioned.

  Instance ID:    i-0abc123
  Status:         provisioning (Horde starting up, ~3 min)

  Horde HTTP:     http://<private-ip>:5000
  Horde gRPC:     <private-ip>:5002

  Credentials:    .fabrica/horde-credentials.yaml

  Note: Horde is accessible via the instance's private IP. Ensure your
        machine can reach it (VPN, VPC peering, or same-VPC access).
        To allow broader access, update horde.allowedCidr in fabrica.yaml.

Next steps:
  1. fabrica horde status -w       Wait for coordinator to become ready
  2. Open http://<private-ip>:5000 Complete admin account setup in the web UI
  3. fabrica horde submit <file>   Submit a BuildGraph job
```

If `AllowedCIDR == "0.0.0.0/0"`, append:
```
  WARNING: horde.allowedCidr is 0.0.0.0/0 — ports 5000 and 5002 are open
           to the internet. Restrict this in fabrica.yaml before connecting
           agents or running production workloads.
```

### Step 3: Wire into `cmd/horde/horde.go` and `cmd/root/root.go`

```go
// cmd/horde/horde.go
package horde

import (
    "io"
    "github.com/jpvelasco/fabrica/cmd/globals"
    "github.com/jpvelasco/fabrica/cmd/horde/create"
    "github.com/spf13/cobra"
)

func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "horde",
        Short: "Manage Unreal Horde build coordinator",
    }
    cmd.AddCommand(create.New(runtimeSource, optionsSource, out))
    return cmd
}
```

Add to `cmd/root/root.go`:
```go
import "github.com/jpvelasco/fabrica/cmd/horde"
// ...
cmd.AddCommand(horde.New(runtimeSource, optionsSource, out))
```

### Step 4: Run tests

Run: `go test ./cmd/horde/create/... && go test ./internal/horde/...`
Expected: PASS

### Step 5: Commit

```bash
git add cmd/horde/ internal/horde/ internal/config/config.go cmd/root/root.go
git commit -m "feat: add fabrica horde create command"
```
