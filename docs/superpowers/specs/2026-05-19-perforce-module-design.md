# Perforce Module Design

**Date:** 2026-05-19  
**Status:** Approved — revised scope  
**Scope:** First PR — `fabrica perforce create` (+ `--dry-run`) and `fabrica perforce status`

---

## Revision Note

First-PR scope narrowed from original design. IAM role/instance profile creation, SSM Parameter Store for admin password, and S3 journal backup are deferred to later PRs. This keeps the first slice focused on validating the Cloud Control provisioning path, state tracking, and cost estimation without introducing IAM orchestration complexity.

---

## 1. Module Structure

```
cmd/perforce/
    perforce.go               # root subcommand, registers create + status

    create/
        create.go             # fabrica perforce create
        create_test.go        # white-box tests (package create)
        cobra_test.go         # black-box Cobra-layer tests (package create_test)

    status/
        status.go             # fabrica perforce status
        status_test.go        # white-box tests

internal/perforce/
    config.go                 # PerforceConfig struct (maps to fabrica.yaml perforce: section)
    plan.go                   # CreatePlan type: resolved names, resource specs, cost inputs
    resources.go              # desired-state builders for SG, instance, DLM
    userdata.go               # cloud-init script generation (Go template)
    userdata_test.go          # pure string verification — no AWS
    cost.go                   # estimators registered against cost.Global via init()
    cost_test.go
    plan_test.go
```

`internal/perforce` has no AWS SDK imports. It produces plans and scripts; `internal/cloud/aws` executes them.

---

## 2. First Deliverable Scope

### Cloud Control resources

| TypeName | Purpose |
|---|---|
| `AWS::EC2::SecurityGroup` | Allow TCP 1666 inbound; restrict SSH to admin CIDR or disable |
| `AWS::EC2::Instance` | Helix Core server; EBS data volume attached at launch |
| `AWS::DLM::LifecyclePolicy` | Daily EBS snapshot, retain 7 days |

### `fabrica perforce create`

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--dry-run` | false | Print plan + cost estimate; zero AWS calls |
| `--yes` | false | Skip confirmation prompt |
| `--instance-type` | `m5.xlarge` | EC2 instance type |
| `--version` | `"2024.2"` | Helix Core version: `"latest"`, `"2024.2"`, or `"2024.2/2659294"` |
| `--volume-size` | `500` | EBS data volume size in GiB |

Version precedence (highest to lowest):
1. `--version` CLI flag
2. `perforce.version` in `fabrica.yaml`
3. Built-in default (`"2024.2"`)

**Dry-run output:**

```
Perforce Helix Core (dry run)
----------------------------------------------------------
  AWS account:      123456789012
  AWS region:       us-east-1
  Instance type:    m5.xlarge
  Helix Core:       2024.2 (pinned)
  Data volume:      500 GiB gp3
  Snapshots:        daily, retain 7 days

Resources to create:
  Security Group:   fabrica-perforce-sg
  EC2 Instance:     fabrica-perforce
  DLM Policy:       fabrica-perforce-snapshots

Cost estimate:
----------------------------------------------------------
  Resource                     Cost/mo    Confidence
----------------------------------------------------------
  EC2 m5.xlarge (on-demand)    $140.16    high
  EBS gp3 500 GiB              $ 40.00    high
  DLM snapshots (estimated)    $  5.00    medium
----------------------------------------------------------
  Total:                       $185.16
Confidence: medium

Run without --dry-run to proceed.
```

**Apply flow:**

1. Resolve identity (account, region) via `provider.Identity`
2. Build `CreatePlan` from config + flags (resolves default VPC if `vpcId`/`subnetId` not configured)
3. Print plan summary
4. Check state — abort cleanly if `"perforce"` module already exists
5. Confirm: exact phrase `"create perforce <account>"` unless `--yes`
6. Acquire state lock (`"perforce"`)
7. Generate admin password (`crypto/rand`, 24-char alphanumeric); write to `.fabrica/perforce-credentials.yaml` (mode 0600); print once to terminal
8. Create Security Group → poll until active; record identifier in state
9. Create EC2 Instance (user data embedded) → poll until running; record identifier in state
10. Create DLM Lifecycle Policy; record identifier in state
11. Write `ModuleState`: module `"perforce"`, status `"provisioning"`, all 3 resource identifiers
12. Release lock
13. Print connection info + readiness note

**Admin password handling:** No SSM, no IAM in this PR. The password is generated locally, stored in `.fabrica/perforce-credentials.yaml` (gitignored, mode 0600), and embedded in user data. This is a bootstrap credential — users are expected to rotate it after first login. A warning is printed at create time:

```
Admin credentials written to .fabrica/perforce-credentials.yaml
Keep this file secure. Rotate the password after first login.
```

The password appears in user data (EC2 metadata). This is a known V1 limitation; SSM-based secret delivery is the V2 improvement once IAM provisioning is implemented.

**Already-exists behavior:**

```
Perforce is already provisioned. Run 'fabrica perforce status' to check health.
Use 'fabrica perforce destroy' to remove it first.
```

Exit cleanly (no error).

**Post-create output:**

```
Perforce Helix Core provisioned.

  Instance ID:   i-0abc123def456789
  P4PORT:        tcp:10.0.1.42:1666
  Status:        provisioning (Helix Core setup in progress, ~3 min)

  Admin credentials: .fabrica/perforce-credentials.yaml
  Warning: Rotate the admin password after first login.

Next steps:
  fabrica perforce status      Check readiness
```

Note: P4PORT uses plain TCP in this PR (not SSL). SSL requires cert generation in user data which adds complexity. TCP is acceptable for V1 evaluation use; the doc string on the command notes this limitation explicitly.

### `fabrica perforce status`

Reads the `"perforce"` module from state, then calls `provider.Resources().Get()` for the instance.

**Output (provisioning — instance running, user data still executing):**

```
Perforce Helix Core
  Status:        provisioning
  Instance ID:   i-0abc123def456789  (running)
  Instance type: m5.xlarge
  Private IP:    10.0.1.42
  P4PORT:        tcp:10.0.1.42:1666
  Helix Core:    setting up... (~3 min from launch)
  Snapshots:     daily (retain 7)
```

**Output (ready — detected via TCP port probe):**

```
Perforce Helix Core
  Status:        ready
  Instance ID:   i-0abc123def456789  (running)
  Instance type: m5.xlarge
  Private IP:    10.0.1.42
  P4PORT:        tcp:10.0.1.42:1666
  Helix Core:    2024.2 (responding)
  Snapshots:     daily (retain 7)
```

**Output (not provisioned):**

```
Perforce is not provisioned. Run 'fabrica perforce create' to set it up.
```

**Readiness detection without SSM:** Status transitions from `provisioning` to `ready` when a TCP dial to port 1666 succeeds (via `net.DialTimeout`). This is a lightweight probe from the CLI machine — it requires network connectivity to the instance's private IP, which is typical in a studio environment. If the CLI machine cannot reach the instance IP (e.g. no VPN), status shows `provisioning` with a note:

```
  Helix Core:    unreachable from this machine (check VPN/network)
```

State is updated to `"ready"` when the probe succeeds; status always re-probes.

**`--wait` / `-w` flag:** Poll every 15 seconds until `ready` or until 10 minutes elapses.

---

## 3. Key Implementation Details

### User data / cloud-init

`userdata.go` exposes a `Generate(cfg UserDataConfig) (string, error)` function that renders a `text/template`. The template produces a `#!/bin/bash` script with `set -euo pipefail`.

```go
type UserDataConfig struct {
    Version      string // "latest", "2024.2", or "2024.2/2659294"
    ServerID     string // e.g. "fabrica-perforce"
    AdminPass    string // generated by create command, embedded in script
    DataDevice   string // e.g. "/dev/nvme1n1"
    DataMount    string // "/hxdepots"
}
```

Key script sections:

```bash
#!/bin/bash
set -euo pipefail

# Add Perforce package repo (Ubuntu 22.04 LTS)
wget -qO - https://package.perforce.com/perforce.pubkey | apt-key add -
add-apt-repository "deb http://package.perforce.com/apt/ubuntu jammy release"
apt-get update -qq

# Install Helix Core
{{ if eq .Version "latest" -}}
apt-get install -y helix-p4d
{{- else -}}
apt-get install -y "helix-p4d={{ .Version }}"
{{- end }}

# Format and mount data volume
mkfs.ext4 {{ .DataDevice }}
mkdir -p {{ .DataMount }} /hxlogs /hxmetadata
mount {{ .DataDevice }} {{ .DataMount }}
echo "{{ .DataDevice }} {{ .DataMount }} ext4 defaults,nofail 0 2" >> /etc/fstab

# Configure Helix Core
/opt/perforce/sbin/configure-helix-p4d.sh \
  -n {{ .ServerID }} \
  -p 1666 \
  -r {{ .DataMount }} \
  -u admin \
  --super-passwd "{{ .AdminPass }}" \
  -y

# Start service
systemctl enable helix-p4d
systemctl start helix-p4d
```

The admin password in user data is a V1 limitation documented explicitly. V2 replaces it with SSM Parameter Store retrieval once IAM provisioning is added.

### Version handling

Defined in `internal/perforce/config.go`:

```go
const DefaultHelixVersion = "2024.2"
```

Version validation in `NewCreatePlan`:
- `"latest"` → accepted, renders without version pin
- `^\d{4}\.\d+$` (e.g. `"2024.2"`) → accepted, renders as `helix-p4d=2024.2`
- `^\d{4}\.\d+/\d+$` (e.g. `"2024.2/2659294"`) → accepted, renders as `helix-p4d=2024.2/2659294`
- Anything else → error at plan time before any AWS call

### State tracking

After each successful Cloud Control create + poll, `state.UpsertModule` is called with resources created so far and status `"provisioning"`. This means partial state is written on interruption — the next `create` run detects it and prints a clear message rather than attempting a blind re-create.

`ModuleResource` entries for this module:

| TypeName | Identifier |
|---|---|
| `AWS::EC2::SecurityGroup` | `sg-xxxxxxxx` |
| `AWS::EC2::Instance` | `i-xxxxxxxx` |
| `AWS::DLM::LifecyclePolicy` | `policy-xxxxxxxx` |

`status` reads these identifiers from state to call `provider.Resources().Get()`.

### VPC resolution

`PerforceConfig` has optional `VPCId` and `SubnetId` fields. If unset, `NewCreatePlan` calls a `VPCResolver` interface (not raw AWS SDK) injected into the function:

```go
type VPCResolver interface {
    ResolveDefaultVPC(ctx context.Context) (vpcID, subnetID string, err error)
}
```

`awsProvider` implements this via a direct `ec2:DescribeVpcs` SDK call (not Cloud Control — VPC describe is not a mutating operation). The interface keeps `internal/perforce` clean of AWS SDK imports.

### Cost estimation

`internal/perforce/cost.go` registers three estimators via `init()`:

```go
cost.Global.Register("AWS::EC2::Instance", instanceEstimator{})
cost.Global.Register("AWS::EC2::Volume",   volumeEstimator{})
cost.Global.Register("AWS::DLM::LifecyclePolicy", dlmEstimator{})
```

`instanceEstimator` uses a hard-coded on-demand price table for `us-east-1` covering: `m5.large` ($70.08), `m5.xlarge` ($140.16), `m5.2xlarge` ($280.32), `r5.xlarge` ($181.44), `r5.2xlarge` ($362.88). For other regions: `Medium` confidence with a note. For unlisted instance types: error (not a panic).

`volumeEstimator` uses `$0.08/GiB/month` for `gp3`.  
`dlmEstimator` returns `$5.00/month` at `Medium` confidence (snapshot cost depends on data change rate).

---

## 4. Architecture & Integration Points

### What this PR wires for real

| Component | Current state | What this PR does |
|---|---|---|
| `cloudcontrol.go` Create | TODO stub | Fully implements `CreateResource` + token extraction |
| `cloudcontrol.go` Get | TODO stub | Implements `GetResource` for instance/SG state queries |
| `poll.go` WaitForRequest | Unimplemented | Implements exponential backoff on `GetResourceRequestStatus` |
| `state.UpsertModule` | Schema exists | First use by a provisioning command |
| `cost.Global.Register` | 2 Phase 0 entries | First Phase 1 module adds 3 more |
| `config.Perforce any` | Untyped placeholder | Replaced with typed `PerforceConfig` |

### Extensions needed

**`poll.go`:** Exponential backoff, 2s start → 30s ceiling, context-cancellable, configurable total timeout (default 10 min). Must distinguish terminal failure (`FAILED`) from in-progress (`IN_PROGRESS`) and success (`SUCCESS`). Tested independently.

**Cloud Control `CreateResource` response:** The resource identifier arrives via `GetResourceRequestStatus.ResourceModel` after polling, not in the initial response. `ResourceClient.Create(*Resource)` fills in `r.Identifier` on completion. No interface change needed — this is the intended design.

**`awsProvider.ResolveDefaultVPC`:** New method on `awsProvider`, satisfies `perforce.VPCResolver`. Direct `ec2:DescribeVpcs` SDK call with `Filters: [{Name: "isDefault", Values: ["true"]}]`. Not behind Cloud Control.

**`state.GetModuleResource`:** New helper on `*State`:
```go
func (s *State) GetModuleResource(module, typeName string) (*ModuleResource, bool)
```
Used by `status` to look up the instance identifier without scanning the slice manually. Small addition to `internal/state/state.go`.

---

## 5. Testing Strategy

### `internal/perforce` — zero AWS, pure logic

**`plan_test.go`** (table-driven):
- Default config produces correct resource names
- Custom instance type propagates correctly
- Version strings validated: `latest`, `2024.2`, `2024.2/2659294` accepted; `bad`, `""`, `2024` rejected
- Cost resource TypeNames match expected values

**`userdata_test.go`** (string verification):
- `latest` renders without version pin in apt command
- Pinned version renders `helix-p4d=2024.2`
- `helix-p4d=2024.2/2659294` for full version string
- Admin password appears in script exactly once
- Mount point `/hxdepots` present
- `set -euo pipefail` present

**`cost_test.go`**:
- `m5.xlarge` → `$140.16/month`, `High`
- `gp3 500 GiB` → `$40.00/month`, `High`
- DLM → `Medium` confidence
- Unknown instance type → error (not panic)
- Estimators registered only once (duplicate registration panics — verify via recover in test)

### `cmd/perforce/create`

**`create_test.go`** (`package create`) — white-box via `command.run()`:
- Dry-run: zero `ResourceClient.Create` calls; output contains account, region, resource names, cost total
- Already-exists: state has `"perforce"` module → clean exit, correct message, zero AWS calls
- Identity failure: aborts before any AWS call
- Happy path: creates SG → Instance → DLM in order; state written with all 3 identifiers; lock acquired and released
- Partial failure (Instance create fails): SG identifier recorded in state; error returned; lock released
- Confirmation rejection: zero create calls

**`cobra_test.go`** (`package create_test`) — Cobra-layer, same pattern as `cmd/destroy`:
- `--dry-run` produces plan output, zero AWS calls
- `--yes` skips confirmation and executes
- `--version latest` accepted
- `--version bad` returns error before AWS
- Missing provider → clean exit with informative message

### `cmd/perforce/status`

**`status_test.go`** — white-box:
- Not provisioned → "not provisioned" message, clean exit
- State shows `provisioning`, instance running → correct output fields
- TCP probe succeeds → status updated to `ready` in output
- `Get` returns error → error reported clearly, non-zero exit

### `poll.go`

**`poll_test.go`** (new, in `internal/cloud/aws`):
- `SUCCESS` on first poll → returns immediately
- `IN_PROGRESS` then `SUCCESS` → correct number of polls
- `FAILED` → returns error with failure reason
- Context cancellation mid-poll → returns context error
- Backoff ceiling respected (never exceeds 30s interval)

---

## 6. Risks

### Password in user data (V1 limitation)

The admin password is embedded in EC2 user data. User data is accessible to anyone with `ec2:DescribeInstanceAttribute` on the instance. This is a known, documented limitation of this PR. Mitigation in V1: IAM permissions for Fabrica's AWS user should restrict who can call `DescribeInstanceAttribute`. Full mitigation (SSM Parameter Store) is V2 once IAM provisioning lands.

Fabrica prints a clear warning at create time and in `status`:

```
Warning: Admin password is stored in EC2 user data. Restrict DescribeInstanceAttribute
access and rotate the password after first login. See 'fabrica perforce status' for details.
```

### Cloud Control for `AWS::EC2::Instance`

Cloud Control supports EC2 instance creation but is known to be slower and occasionally less reliable than direct EC2 SDK calls for instance management. For V1 create-only flow, this is acceptable risk. The `poll.go` implementation must handle transient errors (throttling, `IN_PROGRESS` loops) gracefully.

If Cloud Control proves unreliable for EC2 specifically during implementation, the fallback is a direct `ec2:RunInstances` call behind the same `ResourceClient.Create` interface — `internal/perforce` changes nothing, only `internal/cloud/aws` changes routing for `AWS::EC2::Instance`.

### TCP readiness probe requires network access

`fabrica perforce status` probes TCP 1666 from the CLI machine. In a studio environment with VPN this is standard. For users running Fabrica from outside the VPC (e.g. from a laptop with no VPN), the probe will time out and status will show `unreachable`. This is informative, not an error — the instance may still be ready. Documented behavior, not a blocker.

### Default VPC security posture

Using the default VPC places the instance in a subnet that may have internet access. For evaluation this is acceptable. The dry-run output includes a note:

```
  VPC:          default (vpc-xxxxxxxx)
  Note: Default VPC used. For production, configure a dedicated VPC with private subnets.
```
