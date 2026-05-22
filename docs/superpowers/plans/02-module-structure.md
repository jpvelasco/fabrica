# Horde V1 — Module Structure

## Folder Layout

```
cmd/horde/
  horde.go                       # parent command; wires create/status/submit
  create/
    create.go                    # command struct, New(), run(), applyCreate(), print* helpers
    create_test.go               # white-box: run() paths, seams (package create)
    cobra_test.go                # black-box: flag parsing, command construction (package create_test)
  status/
    status.go                    # command struct, New(), run(), buildInfo(), pollUntilReady()
    status_test.go               # white-box (package status)
    cobra_test.go                # black-box (package status_test)
  submit/
    client.go                    # HordeClient interface + concrete HTTP implementation
    submit.go                    # command struct, New(), run(), applySubmit()
    submit_test.go               # white-box (package submit)
    cobra_test.go                # black-box (package submit_test)

internal/horde/
  config.go                      # HordeConfig struct, VPCResolver interface
  plan.go                        # CreatePlan, NewCreatePlan
  resources.go                   # SGDesiredState, InstanceDesiredState (Cloud Control JSON)
  userdata.go                    # cloud-init template; Generate() + GenerateRaw()
  cost.go                        # m7i.* EC2 + gp3 EBS estimators; registers via init()
  buildgraph.go                  # ParseBuildGraph(path) → *BuildGraphJob; pure XML parse
  config_test.go                 # (if needed for VPCResolver interface)
  plan_test.go                   # NewCreatePlan validation
  resources_test.go              # SGDesiredState + InstanceDesiredState JSON shape
  userdata_test.go               # GenerateRaw content; Generate returns base64
  cost_test.go                   # estimator round-trip
  buildgraph_test.go             # XML parse happy path + error cases
```

**Files modified outside these directories (one only):**

```
internal/config/config.go        # Config.Horde any → HordeConfig; fileConfig updated
```

**Root wiring (one line added):**

```
cmd/root/root.go                 # cmd.AddCommand(horde.New(...))
```

---

## Package Responsibilities

### `internal/horde` — pure plan layer (no AWS SDK)

| File | Responsibility |
|---|---|
| `config.go` | `HordeConfig` struct with all YAML fields; `VPCResolver` interface so the AWS provider can be called without importing the SDK here |
| `plan.go` | `CreatePlan` struct; `NewCreatePlan()` validates inputs, applies defaults, calls `VPCResolver` when VPC not configured; `CostResources` slice for cost registry |
| `resources.go` | `SGDesiredState()` and `InstanceDesiredState()` return `json.RawMessage` for Cloud Control — no SDK types |
| `userdata.go` | `UserDataConfig` struct; `Generate()` returns base64-encoded cloud-init; `GenerateRaw()` returns plain string for tests |
| `cost.go` | `m7i.*` EC2 price table + gp3 EBS estimator; both registered in `init()` against `cost.Global` |
| `buildgraph.go` | `BuildGraphJob` struct; `ParseBuildGraph(path string) (*BuildGraphJob, error)` — opens file, parses XML, no HTTP/AWS |

### `cmd/horde` — execution layer

| File | Responsibility |
|---|---|
| `horde.go` | Parent `cobra.Command`; adds create/status/submit subcommands |
| `create/create.go` | Reads config, builds `CreatePlan`, confirms with user, calls `createResource` for SG then instance, writes credentials, writes state |
| `status/status.go` | Reads state, calls Cloud Control Get for live EC2 data, TCP-probes port 5000, transitions provisioning→ready, prints text or JSON |
| `submit/client.go` | `HordeClient` interface; `hordeHTTPClient` concrete implementation resolving coordinator IP via Cloud Control, POSTing to Horde REST API |
| `submit/submit.go` | Parses BuildGraph file, resolves Horde URL, calls `hordeClient.SubmitJob()`, optionally polls with `--wait` |

---

## HordeConfig (in `internal/horde/config.go`)

```go
type HordeConfig struct {
    AmiID        string `mapstructure:"amiId"        yaml:"amiId"`
    InstanceType string `mapstructure:"instanceType" yaml:"instanceType"` // default: m7i.xlarge
    VolumeSize   int    `mapstructure:"volumeSize"   yaml:"volumeSize"`   // default: 100
    VPCId        string `mapstructure:"vpcId"        yaml:"vpcId"`
    SubnetId     string `mapstructure:"subnetId"     yaml:"subnetId"`
    Port         int    `mapstructure:"port"         yaml:"port"`         // default: 5000
    GRPCPort     int    `mapstructure:"grpcPort"     yaml:"grpcPort"`     // default: 5002
    AllowedCIDR  string `mapstructure:"allowedCidr"  yaml:"allowedCidr"` // default: 10.0.0.0/8
}

type VPCResolver interface {
    ResolveDefaultVPC(ctx context.Context) (vpcID, subnetID string, err error)
}
```

`AmiID` has no default. `NewCreatePlan` returns an error immediately if empty:
```
horde.amiId is required. Provide an AMI ID that contains MongoDB, Redis,
and the Horde server. See: https://github.com/jpvelasco/fabrica/blob/main/docs/horde-ami.md
```

---

## CreatePlan (in `internal/horde/plan.go`)

```go
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

    SGName       string  // "fabrica-horde-sg"
    InstanceName string  // "fabrica-horde"

    CostResources []cost.Resource
}
```

---

## Config change (`internal/config/config.go`)

```go
// Before:
Horde any `mapstructure:"horde" yaml:"horde"`

// After:
Horde horde.HordeConfig `mapstructure:"horde" yaml:"horde"`
```

Also update `fileConfig` struct and `fileConfig()` method to use `horde.HordeConfig` instead of `any`. Remove `emptySection()` call for Horde (it's now a typed struct, not `any`).

Import: `"github.com/jpvelasco/fabrica/internal/horde"` added to `internal/config/config.go`.

---

## BuildGraphJob (in `internal/horde/buildgraph.go`)

```go
type BuildGraphJob struct {
    Name   string
    Target string
}

func ParseBuildGraph(path string) (*BuildGraphJob, error)
```

Parses the `<BuildGraph>` root element and first `<Agent>` or `<Node>` to extract Name/Target. Pure file I/O + XML decode — no AWS, no HTTP.
