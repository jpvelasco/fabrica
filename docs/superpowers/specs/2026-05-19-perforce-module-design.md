# Perforce Module Design

**Date:** 2026-05-19  
**Status:** Approved  
**Scope:** First PR — `fabrica perforce create` (+ `--dry-run`) and `fabrica perforce status`

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
    resources.go              # resource definitions: SG, instance, DLM (TypeNames + desired state builders)
    userdata.go               # cloud-init script generation (Go template)
    userdata_test.go          # pure string verification tests
    cost.go                   # cost estimators registered against cost.Global via init()
    cost_test.go
    plan_test.go
```

`internal/perforce` has no AWS SDK imports. It produces plans and user data scripts; the AWS layer (`internal/cloud/aws`) executes them.

---

## 2. First Deliverable Scope

### `fabrica perforce create`

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `--dry-run` | false | Print plan + cost estimate, no AWS calls |
| `--yes` | false | Skip confirmation prompt |
| `--instance-type` | `m5.xlarge` | EC2 instance type |
| `--version` | `"2024.2"` (pinned at release) | Helix Core version: `"latest"`, `"2024.2"`, or `"2024.2/2659294"` |
| `--volume-size` | `500` | EBS data volume size in GiB |

Version precedence (highest to lowest):
1. `--version` CLI flag
2. `perforce.version` in `fabrica.yaml`
3. Built-in default (`"2024.2"`)

**Dry-run output (example):**

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

**Apply flow (sequential, each step gated on previous):**

1. Resolve identity (account, region) via `provider.Identity`
2. Build `CreatePlan` from config + flags (includes VPC resolution if not configured)
3. Print plan summary
4. Check state — abort if `"perforce"` module already exists in state
5. Confirm: exact phrase `"create perforce <account>"` (unless `--yes`)
6. Acquire state lock (`"perforce"` lock ID)
7. Create SSM Parameter (admin password, SecureString) → record identifier
8. Create IAM Role + InstanceProfile → poll until active, record identifiers
9. Create Security Group → poll until active, record identifier
10. Create EC2 Instance (user data embedded, instance profile attached) → poll until running, record identifier
11. Create DLM Lifecycle Policy → record identifier
12. Write `ModuleState` entry: module `"perforce"`, status `"provisioning"`, all 6 resources
13. Release lock
14. Print connection info + readiness note

**Post-create output:**
```
Perforce Helix Core provisioned.

  Instance ID:   i-0abc123def456789
  P4PORT:        ssl:10.0.1.42:1666
  Status:        provisioning (Helix Core setup in progress, ~3 min)

Next steps:
  fabrica perforce status      Check readiness
  fabrica perforce status -w   Wait until ready (polls until Helix Core responds)
```

**Already-exists behavior:** If state shows `"perforce"` module already exists, print:
```
Perforce is already provisioned. Run 'fabrica perforce status' to check health.
Use 'fabrica perforce destroy' to remove it first.
```
Exit cleanly (no error).

### `fabrica perforce status`

Reads the `"perforce"` module from state, then queries AWS for current resource state.

**Output (provisioning):**
```
Perforce Helix Core
  Status:          provisioning
  Instance:        i-0abc123def456789  (running)
  Instance type:   m5.xlarge
  Private IP:      10.0.1.42
  P4PORT:          ssl:10.0.1.42:1666
  Helix Core:      setting up... (check back in ~2 min)
  Snapshots:       daily (retain 7)
```

**Output (ready):**
```
Perforce Helix Core
  Status:          ready
  Instance:        i-0abc123def456789  (running)
  Instance type:   m5.xlarge
  Private IP:      10.0.1.42
  P4PORT:          ssl:10.0.1.42:1666
  Helix Core:      2024.2/2659294  (responding)
  Snapshots:       daily (retain 7)
  Last snapshot:   2026-05-18 03:00 UTC
```

**Output (not provisioned):**
```
Perforce is not provisioned. Run 'fabrica perforce create' to set it up.
```

"Ready" detection: SSM `SendCommand` to run `p4 -p ssl:1666 info` and check exit code 0. Status transitions: `provisioning` → `ready` when the command succeeds. State is updated on each `status` call when a transition is detected.

**`--wait` / `-w` flag:** Poll every 15 seconds until status is `ready` (or error). Max wait: 10 minutes.

### Cloud Control resources (V1)

| TypeName | Purpose | Notes |
|---|---|---|
| `AWS::SSM::Parameter` | Admin password (SecureString) | Created before instance |
| `AWS::IAM::Role` | Instance profile role with SSM permissions | Required for status readiness probe |
| `AWS::IAM::InstanceProfile` | Attaches role to instance | Created before instance |
| `AWS::EC2::SecurityGroup` | Allow TCP 1666 inbound, restrict SSH to admin CIDR | VPC-scoped |
| `AWS::EC2::Instance` | Helix Core server with data EBS volume | User data embedded |
| `AWS::DLM::LifecyclePolicy` | Daily EBS snapshot, retain 7 days | Targets instance by tag |

---

## 3. Key Implementation Details

### User data / cloud-init

`userdata.go` defines a `Script` type and a `Generate(cfg UserDataConfig) (string, error)` function. `UserDataConfig` captures everything needed to render the script: version string, server ID, SSM parameter name for the admin password, data directory paths.

The script is a Go `text/template`. Key sections:

```bash
#!/bin/bash
set -euo pipefail

# 1. Add Perforce package repo (Ubuntu 22.04 LTS)
wget -qO - https://package.perforce.com/perforce.pubkey | apt-key add -
add-apt-repository "deb http://package.perforce.com/apt/ubuntu jammy release"
apt-get update -qq

# 2. Install Helix Core
{{ if eq .Version "latest" -}}
apt-get install -y helix-p4d
{{- else -}}
apt-get install -y helix-p4d={{ .Version }}
{{- end }}

# 3. Format and mount data volume
mkfs.ext4 /dev/nvme1n1
mkdir -p /hxdepots /hxlogs /hxmetadata
mount /dev/nvme1n1 /hxdepots
echo "/dev/nvme1n1 /hxdepots ext4 defaults,nofail 0 2" >> /etc/fstab

# 4. Retrieve admin password from SSM (never in user data plaintext)
ADMIN_PASS=$(aws ssm get-parameter \
  --name "{{ .SSMParamName }}" \
  --with-decryption \
  --query Parameter.Value \
  --output text \
  --region {{ .Region }})

# 5. Configure Helix Core
/opt/perforce/sbin/configure-helix-p4d.sh \
  -n {{ .ServerID }} \
  -p ssl:1666 \
  -r /hxdepots \
  -u admin \
  --super-passwd "$ADMIN_PASS" \
  -y

# 6. Enable and start
systemctl enable helix-p4d
systemctl start helix-p4d
```

The admin password is generated at plan time in Go (`crypto/rand`, 24-char alphanumeric), stored as `AWS::SSM::Parameter` (SecureString) *before* instance creation, and retrieved by the script at first boot. This means the SSM parameter creation is step 0 in the apply flow, before the Security Group.

`userdata_test.go` verifies: version branch renders correctly for `latest` vs pinned, SSM parameter name appears exactly once in the output, no literal password in the script, mount point `/hxdepots` present.

### Version handling

`PerforceConfig.Version` is a `string`. Validation in `CreatePlan`:

- `"latest"` → pass through to template
- `"YYYY.N"` (e.g. `"2024.2"`) → validate format with regexp `^\d{4}\.\d+$`, pass through
- `"YYYY.N/NNNNNN"` (e.g. `"2024.2/2659294"`) → validate format, pass through to apt as exact version constraint

Invalid format → error at plan time, before any AWS call.

Default version constant defined in `internal/perforce/config.go`:
```go
const DefaultHelixVersion = "2024.2"
```

### Create flow and state locking

The lock key is `"perforce"`. `state.LockStore.Acquire` is called after confirmation and before the first AWS mutation. If acquisition fails (another `fabrica` invocation is in progress), print:
```
Another Fabrica operation is in progress. Try again in a moment.
```
and exit non-zero.

Resource identifiers are written to state incrementally: after each `Create` + poll completes, `state.UpsertModule` is called with the resources created so far and status `"provisioning"`. If the create command is interrupted mid-flight, the next run detects a partial `"provisioning"` state and reports:
```
Perforce provisioning is incomplete (2 of 3 resources created).
Run 'fabrica perforce create --resume' to continue, or 'fabrica perforce destroy' to clean up.
```
`--resume` is a stretch goal for this PR; the detection and clear message are not.

### Cost estimation

`internal/perforce/cost.go` registers three estimators via `init()`:

```go
func init() {
    cost.Global.Register("AWS::EC2::Instance", instanceEstimator{})
    cost.Global.Register("AWS::EC2::Volume", volumeEstimator{})
    cost.Global.Register("AWS::DLM::LifecyclePolicy", dlmEstimator{})
}
```

`instanceEstimator` accepts a `Properties` map (decoded from `Resource.DesiredState`) and looks up the instance type against a hard-coded pricing table for `us-east-1` (on-demand Linux). For other regions, confidence drops to `Medium` with a note. The table covers the instance types we recommend: `m5.large`, `m5.xlarge`, `m5.2xlarge`, `r5.xlarge`, `r5.2xlarge`.

`volumeEstimator` uses `$0.08/GiB/month` for `gp3` (us-east-1). `dlmEstimator` returns `$5.00 / month` at `Medium` confidence (snapshot storage cost depends on data change rate).

Cost resources in `CreatePlan` carry a `Properties map[string]string` that the estimators read (e.g. `"InstanceType": "m5.xlarge"`, `"VolumeSize": "500"`).

---

## 4. Architecture & Integration Points

### Parts of existing architecture exercised for the first time

| Component | How Perforce exercises it |
|---|---|
| `cloudcontrol.go` Create stub | First real `CreateResource` call — must implement polling |
| `cloudcontrol.go` Get stub | Used by `status` to fetch current instance state |
| `poll.go` WaitForRequest | Must be fully implemented; Perforce create hangs otherwise |
| `state.UpsertModule` | First module beyond the Phase 0 bootstrap resources |
| `cost.Global.Register` (Phase 1) | First registrations beyond the two Phase 0 entries |
| `config.Perforce any` | Replaced with typed `PerforceConfig` — first config struct extension |

### Areas that need extension

**`cloudcontrol.go` Create response → identifier extraction.** Cloud Control's `CreateResource` returns a progress token. After polling completes, `GetResourceRequestStatus` returns the resource identifier. The `ResourceClient.Create(*Resource)` signature fills in `r.Identifier` on success. We implement this fully.

**`poll.go` WaitForRequest.** Implement exponential backoff starting at 2 seconds, doubling to a ceiling of 30 seconds, total timeout configurable (default 10 minutes for instance creation). Respects `context.Context` cancellation.

**VPC resolution.** `PerforceConfig` accepts optional `VPCId` and `SubnetId`. If unset, the AWS provider resolves the default VPC via `ec2:DescribeVpcs` (a direct SDK call, not Cloud Control). This is added to `awsProvider` as a `ResolveDefaultVPC(ctx) (vpcID, subnetID string, err error)` method — similar to how `StateBackendChecker` was added. `internal/perforce` never calls the SDK directly; it gets VPC IDs through the `CreatePlan` function which accepts a `VPCResolver` interface.

**SSM parameter creation.** Admin password stored in SSM before instance launch. Needs a new `SSMClient` in the AWS provider, or direct use of `AWS::SSM::Parameter` via Cloud Control. We use Cloud Control for consistency.

**State `GetModuleResource` helper.** The `status` command needs to find a specific resource in the module state (e.g. the instance identifier). Add a `(s *State) GetModuleResource(module, typeName string) (*ModuleResource, bool)` helper to `internal/state/state.go`.

### Dependency flow stays clean

`internal/perforce` only imports `internal/config`, `internal/cost`, `internal/cloud` (interface), and `internal/state` (types). No AWS SDK imports. `cmd/perforce/create` imports `internal/perforce` and `internal/cloud`. The one-way flow is preserved.

---

## 5. Testing Strategy

### `internal/perforce` (no AWS, pure logic)

**`plan_test.go`** — table-driven:
- Default config produces correct resource names (`fabrica-perforce-sg`, `fabrica-perforce`, `fabrica-perforce-snapshots`)
- Custom instance type propagates to plan
- Version validation: `"latest"` accepted, `"2024.2"` accepted, `"2024.2/2659294"` accepted, `"bad"` returns error
- Cost resources match expected TypeNames and properties

**`userdata_test.go`** — pure string:
- `latest` version renders without version pin in apt command
- Pinned version renders `helix-p4d=2024.2` in apt command
- SSM parameter name appears in script
- No literal password string in output
- `/hxdepots` mount present

**`cost_test.go`**:
- `m5.xlarge` produces `$140.16/month` at `High` confidence
- `gp3 500 GiB` produces `$40.00/month` at `High` confidence
- DLM produces `Medium` confidence
- Unknown instance type returns error (not a panic)

### `cmd/perforce/create`

**`create_test.go`** (`package create`) — white-box via `command.run()`:
- Dry-run: zero `ResourceClient.Create` calls, output contains plan fields
- Already-exists: state has `"perforce"` module → clean exit with informative message
- Identity failure: aborts before any create call
- Apply happy path: creates SG → Instance → DLM in order, state written, lock released
- Partial failure (Instance create fails): SG identifier in state, error returned, lock released
- Confirmation rejection: no create calls made

**`cobra_test.go`** (`package create_test`) — Cobra-layer:
- `--dry-run` zero AWS calls
- `--yes` skips confirmation and executes
- `--version latest` accepted, `--version bad` returns error before AWS
- Missing provider handled gracefully

### `cmd/perforce/status`

**`status_test.go`** — white-box:
- Not-provisioned: outputs creation prompt
- Provisioning state: outputs instance ID, IP, "setting up" message
- Ready state: outputs all fields including Helix Core version
- AWS error on Get: reports error clearly

### Mocking strategy

All `cmd/` tests use a `fakeResourceClient` implementing `cloud.ResourceClient`. It tracks calls by TypeName, controls return values (success, error, specific identifiers). The `fakeProvider` from destroy tests is the template. No real AWS credentials or network calls in any unit test.

---

## 6. Risks and Open Questions

### Cloud Control for `AWS::EC2::Instance`

Cloud Control supports `AWS::EC2::Instance` but has known gaps: user data is only applied at creation (cannot be updated via patch), and some instance-level attributes require instance stop/start cycles that Cloud Control doesn't model well. For V1 (create-only, no updates), this is acceptable. If future phases need in-place instance updates (e.g. resize), we may fall back to direct `ec2` SDK calls behind the same `ResourceClient` interface — callers don't change.

**Mitigation:** Keep the `cloud.ResourceClient` interface clean. The AWS provider can route specific TypeNames to direct SDK implementations without changing the interface.

### Cloud Control polling reliability

Cloud Control's async model means `CreateResource` returns a token and we poll `GetResourceRequestStatus`. In practice, EC2 instance creation takes 60-120 seconds; the polling loop must handle transient errors (throttling, network blips) without failing. The exponential backoff in `poll.go` must be robust.

**Mitigation:** Implement `poll.go` with context cancellation, exponential backoff (2s → 30s ceiling), and clear timeout errors. Test the backoff logic independently.

### Default VPC assumption

Using the default VPC is fine for evaluation but has security implications (public subnets in the default VPC). V1 documentation must be clear: "Default VPC is suitable for evaluation. Production deployments should configure a dedicated VPC with private subnets."

### Helix Core `configure-helix-p4d.sh` availability

The Perforce-provided setup script (`configure-helix-p4d.sh`) is included with the `helix-p4d` package. Its CLI arguments and behavior should be treated as a dependency. We should pin to an Ubuntu LTS release (22.04 Jammy) to avoid unexpected package repository changes.

### Status "ready" detection via SSM

`fabrica perforce status` checks readiness by running `p4 -p ssl:1666 info` via SSM Run Command. This requires:
- Instance has an IAM instance profile with `ssm:SendCommand` permission
- SSM agent is running on the instance (installed by default on Ubuntu 22.04 LTS AMIs)

The instance profile is a fourth resource not listed in the main resources table: `AWS::IAM::Role` + `AWS::IAM::InstanceProfile`. These need to be created before the instance. This is a gap in the initial scope list that must be resolved during implementation — either add them as tracked resources or use a pre-existing instance profile from config.

**Decision needed before implementation:** Create IAM role/profile as part of `perforce create` (adds two more Cloud Control resources and IAM permissions), or require a pre-created instance profile ARN in `PerforceConfig`. Recommendation: create it as part of `perforce create` for zero-friction setup. This adds `AWS::IAM::Role` and `AWS::IAM::InstanceProfile` to the Cloud Control resource list.

### Resolved decisions

1. **IAM instance profile**: Created via Cloud Control as part of `perforce create`. Added to resource table above.
2. **Ubuntu AMI**: SSM Parameter Store lookup (`/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id`) at plan time, with a fallback table for the regions Fabrica explicitly supports. This keeps AMI IDs current without requiring manual maintenance.
3. **P4PORT protocol**: SSL from day one. Plain TCP is not offered — it would be a security regression relative to the CGD Toolkit and undermines Fabrica's differentiation. SSL cert is self-signed and generated by `configure-helix-p4d.sh` during first boot.
