# Perforce Module Design

**Date:** 2026-05-19  
**Status:** Approved — final scope  
**Scope:** First PR — `fabrica perforce create` (+ `--dry-run`) and `fabrica perforce status`

---

## Revision History

- **Rev 1:** Full scope (IAM, SSM, DLM, SSL)
- **Rev 2:** Removed IAM, SSM, SSL; kept DLM
- **Rev 3 (current):** Removed DLM. First PR is SG + EC2 instance only. Keeps the first slice maximally focused on validating Cloud Control provisioning, state tracking, and cost estimation.

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
    resources.go              # desired-state builders for SG and instance
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
| `AWS::EC2::SecurityGroup` | Allow TCP 1666 inbound; no inbound SSH by default |
| `AWS::EC2::Instance` | Helix Core server; gp3 EBS data volume attached at launch |

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

Resources to create:
  Security Group:   fabrica-perforce-sg
  EC2 Instance:     fabrica-perforce

Cost estimate:
----------------------------------------------------------
  Resource                     Cost/mo    Confidence
----------------------------------------------------------
  EC2 m5.xlarge (on-demand)    $140.16    high
  EBS gp3 500 GiB              $ 40.00    high
----------------------------------------------------------
  Total:                       $180.16
Confidence: high

Run without --dry-run to proceed.
```

**Apply flow:**

1. Resolve identity (account, region) via `provider.Identity`
2. Build `CreatePlan` from config + flags (resolves default VPC if `vpcId`/`subnetId` not configured)
3. Print plan summary
4. Check state — abort cleanly if `"perforce"` module already exists
5. Confirm: exact phrase `"create perforce <account>"` unless `--yes`
6. Acquire state lock (`"perforce"`)
7. Generate admin password (`crypto/rand`, 24-char alphanumeric); write to `.fabrica/perforce-credentials.yaml` (mode 0600); print warning
8. Create Security Group → poll until active; record identifier in state
9. Create EC2 Instance (user data embedded) → poll until running; record identifier in state
10. Write `ModuleState`: module `"perforce"`, status `"provisioning"`, both resource identifiers
11. Release lock
12. Print connection info + readiness note

**Admin password handling:** Generated locally via `crypto/rand`, written to `.fabrica/perforce-credentials.yaml` (mode 0600, gitignored), and embedded in user data. V1 limitation — password is visible in EC2 user data to anyone with `ec2:DescribeInstanceAttribute`. Warning printed at create time:

```
Admin credentials written to .fabrica/perforce-credentials.yaml
Warning: Rotate the admin password after first login.
         Restrict ec2:DescribeInstanceAttribute to limit exposure.
```

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

### `fabrica perforce status`

Reads the `"perforce"` module from state; calls `provider.Resources().Get()` for the instance; probes TCP 1666 for readiness.

**Output (provisioning):**

```
Perforce Helix Core
  Status:        provisioning
  Instance ID:   i-0abc123def456789  (running)
  Instance type: m5.xlarge
  Private IP:    10.0.1.42
  P4PORT:        tcp:10.0.1.42:1666
  Helix Core:    setting up... (~3 min from launch)
```

**Output (ready):**

```
Perforce Helix Core
  Status:        ready
  Instance ID:   i-0abc123def456789  (running)
  Instance type: m5.xlarge
  Private IP:    10.0.1.42
  P4PORT:        tcp:10.0.1.42:1666
  Helix Core:    2024.2 (responding)
```

**Output (not provisioned):**

```
Perforce is not provisioned. Run 'fabrica perforce create' to set it up.
```

**Readiness detection:** TCP dial to port 1666 via `net.DialTimeout` (3s timeout). No SSM, no IAM required. If unreachable from the CLI machine, output notes:

```
  Helix Core:    unreachable from this machine (check VPN/network)
```

State is updated to `"ready"` when the probe first succeeds; subsequent `status` calls re-probe each time.

**`--wait` / `-w` flag:** Poll every 15 seconds until `ready` or 10 minutes elapsed.

---

## 3. Key Implementation Details

### User data / cloud-init

`userdata.go` exposes `Generate(cfg UserDataConfig) (string, error)`, rendering a `text/template`:

```go
type UserDataConfig struct {
    Version    string // "latest", "2024.2", "2024.2/2659294"
    ServerID   string // e.g. "fabrica-perforce"
    AdminPass  string // generated by create command
    DataDevice string // e.g. "/dev/nvme1n1"
    DataMount  string // "/hxdepots"
}
```

Script (abbreviated):

```bash
#!/bin/bash
set -euo pipefail

wget -qO - https://package.perforce.com/perforce.pubkey | apt-key add -
add-apt-repository "deb http://package.perforce.com/apt/ubuntu jammy release"
apt-get update -qq

{{ if eq .Version "latest" -}}
apt-get install -y helix-p4d
{{- else -}}
apt-get install -y "helix-p4d={{ .Version }}"
{{- end }}

mkfs.ext4 {{ .DataDevice }}
mkdir -p {{ .DataMount }} /hxlogs /hxmetadata
mount {{ .DataDevice }} {{ .DataMount }}
echo "{{ .DataDevice }} {{ .DataMount }} ext4 defaults,nofail 0 2" >> /etc/fstab

/opt/perforce/sbin/configure-helix-p4d.sh \
  -n {{ .ServerID }} -p 1666 -r {{ .DataMount }} \
  -u admin --super-passwd "{{ .AdminPass }}" -y

systemctl enable helix-p4d
systemctl start helix-p4d
```

### Version handling

Constant in `internal/perforce/config.go`:

```go
const DefaultHelixVersion = "2024.2"
```

Validated in `NewCreatePlan` before any AWS call:
- `"latest"` → accepted, no version pin in apt
- `^\d{4}\.\d+$` → accepted (e.g. `"2024.2"`)
- `^\d{4}\.\d+/\d+$` → accepted (e.g. `"2024.2/2659294"`)
- Anything else → error returned immediately

### State tracking

After each successful Cloud Control create + poll:
- Call `state.UpsertModule` with resources created so far, status `"provisioning"`
- On interruption, next `create` run detects partial state and reports it clearly

Final state entry:

```json
{
  "name": "perforce",
  "status": "provisioning",
  "resources": [
    {"typeName": "AWS::EC2::SecurityGroup", "identifier": "sg-xxxxxxxx"},
    {"typeName": "AWS::EC2::Instance",      "identifier": "i-xxxxxxxx"}
  ]
}
```

`status` command reads these identifiers to call `Get` and probe readiness.

New helper added to `internal/state/state.go`:

```go
func (s *State) GetModuleResource(module, typeName string) (*ModuleResource, bool)
```

### VPC resolution

`PerforceConfig` has optional `VPCId` and `SubnetId`. If unset, `NewCreatePlan` calls:

```go
type VPCResolver interface {
    ResolveDefaultVPC(ctx context.Context) (vpcID, subnetID string, err error)
}
```

`awsProvider` implements this via `ec2:DescribeVpcs` (direct SDK call, not Cloud Control). `internal/perforce` never imports AWS SDK.

### Cloud Control wiring (what this PR fully implements)

- `cloudcontrol.go` `Create`: calls `CreateResource`, extracts progress token, polls via `WaitForRequest`, fills `r.Identifier` on completion
- `cloudcontrol.go` `Get`: calls `GetResource`, unmarshals response into `r.ActualState`
- `poll.go` `WaitForRequest`: exponential backoff 2s → 30s ceiling, context-cancellable, 10-minute default timeout; handles `IN_PROGRESS`, `SUCCESS`, `FAILED`

---

## 4. Testing Strategy

### `internal/perforce` — zero AWS, pure logic

**`plan_test.go`** (table-driven):
- Default config → correct resource names (`fabrica-perforce-sg`, `fabrica-perforce`)
- Custom instance type propagates to plan
- Version validation: `"latest"`, `"2024.2"`, `"2024.2/2659294"` accepted; `"bad"`, `""`, `"2024"` rejected
- Cost resource TypeNames match `AWS::EC2::Instance` and `AWS::EC2::Volume`

**`userdata_test.go`** (string verification):
- `"latest"` renders without version pin
- `"2024.2"` renders `helix-p4d=2024.2`
- `"2024.2/2659294"` renders `helix-p4d=2024.2/2659294`
- Admin password appears exactly once
- Mount point `/hxdepots` present
- `set -euo pipefail` present

**`cost_test.go`**:
- `m5.xlarge` → `$140.16/month`, `High`
- `gp3 500 GiB` → `$40.00/month`, `High`
- Unknown instance type → error (not panic)
- Duplicate registration → panics (verify via `recover`)

### `cmd/perforce/create`

**`create_test.go`** (`package create`) — white-box via `command.run()`:
- Dry-run: zero `ResourceClient.Create` calls; output contains account, region, resource names, cost total
- Already-exists: `"perforce"` module in state → clean exit, informative message, zero AWS calls
- Identity failure: aborts before any AWS call
- Happy path: creates SG then Instance in order; state written with both identifiers; lock acquired and released
- Instance create fails: SG identifier recorded in state; error returned; lock released
- Confirmation rejection: zero create calls

**`cobra_test.go`** (`package create_test`) — Cobra-layer, same pattern as `cmd/destroy`:
- `--dry-run` → zero AWS calls, plan output present
- `--yes` → skips confirmation, executes
- `--version latest` → accepted
- `--version bad` → error before any AWS call
- Nil provider → clean exit with informative message

### `cmd/perforce/status`

**`status_test.go`** — white-box:
- Not provisioned → "not provisioned" message, clean exit
- State shows `provisioning`, instance running → correct output fields
- TCP probe succeeds → status shown as `ready`
- `Get` returns error → reported clearly, non-zero exit

### `internal/cloud/aws` — poll logic

**`poll_test.go`** (new):
- `SUCCESS` on first call → returns immediately
- `IN_PROGRESS` then `SUCCESS` → correct number of polls, correct identifier returned
- `FAILED` → returns error with failure reason
- Context cancellation → returns context error
- Backoff ceiling never exceeds 30s (verified via mock clock)

---

## 5. Risks

### Password in user data

Admin password is embedded in EC2 user data, visible to anyone with `ec2:DescribeInstanceAttribute`. Documented limitation; full mitigation (SSM) is a later PR. Fabrica prints a warning at create time and in `status` output.

### Cloud Control reliability for `AWS::EC2::Instance`

Cloud Control's async model adds latency and complexity vs direct EC2 SDK. If Cloud Control proves unreliable for instance creation during implementation, fallback is a direct `ec2:RunInstances` call behind the same `ResourceClient.Create` interface — `internal/perforce` is unaffected.

### TCP readiness probe requires network access

`status` probes TCP 1666 from the CLI machine. Users without VPN access to the instance's private IP will see `unreachable`. Documented behavior; not a blocker.

### Default VPC posture

Dry-run output notes when the default VPC is used:

```
  VPC:   default (vpc-xxxxxxxx)
  Note:  Default VPC used. Configure a dedicated VPC for production.
```
