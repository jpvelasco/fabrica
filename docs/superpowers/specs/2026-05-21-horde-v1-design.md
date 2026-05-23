# Horde V1 Design Spec

**Date:** 2026-05-21
**Status:** Approved
**Scope:** PR #1 — `fabrica horde create`, `fabrica horde status`, `fabrica horde submit`

---

## What We Are Building

First iteration of the Horde module: provision a single Unreal Horde coordinator on EC2, check its health, and submit BuildGraph jobs to it. Agent fleet (Auto Scaling Group) and `fabrica horde destroy` are deferred to PR #2.

---

## Decisions Made

| Decision | Choice | Rationale |
|----------|--------|-----------|
| PR #1 scope | create + status + submit | Coordinator foundation first; agents + destroy in PR #2 |
| Coordinator topology | Single EC2 instance | Coordinator-only V1; agents added later |
| MongoDB | On-instance (same EC2) | No extra AWS cost; production-oriented for small teams |
| Redis | On-instance | Required by Horde for pub/sub; included in AMI |
| submit default behavior | Fire-and-forget; `--wait`/`-w` polls | Fast by default; CI pipelines use `--wait` |
| REST client location | `cmd/horde/submit/` | Only submit needs it in V1; move to `internal/` when a second command needs it |
| Horde installation | AMI-baked by user | Avoids GitHub PAT in UserData; matches how studios manage licensed tools |
| Instance type default | `m7i.xlarge` | Current-gen; `m7i.2xlarge` recommended for production |
| SG default CIDR | `10.0.0.0/8` | Restrictive by default; warning shown if user opens to `0.0.0.0/0` |

---

## Module Structure

```
cmd/horde/
  horde.go                  # parent command; wires create/status/submit
  create/
    create.go
    create_test.go           # white-box: run() paths, seams
    cobra_test.go            # black-box: flag parsing, command construction
  status/
    status.go
    status_test.go
    cobra_test.go
  submit/
    client.go                # HordeClient interface + concrete http implementation
    submit.go
    submit_test.go
    cobra_test.go

internal/horde/
  config.go                  # HordeConfig struct, VPCResolver interface
  plan.go                    # CreatePlan, NewCreatePlan, ResolveVersion
  resources.go               # SGDesiredState, InstanceDesiredState
  userdata.go                # cloud-init template (config + service start only — no software install)
  cost.go                    # m7i.* EC2 + gp3 EBS estimators; registers via init()
  buildgraph.go              # ParseBuildGraph(path) → BuildGraphJob; pure XML parse, no I/O

docs/
  horde-ami.md               # AMI build guide stub (created as part of this PR)
```

`cmd/horde/destroy/` is not created in PR #1. It is added in PR #2.

`internal/config/config.go`: `Config.Horde` is promoted from `any` to `HordeConfig`. This is the only file outside `cmd/horde/` and `internal/horde/` that PR #1 modifies.

---

## HordeConfig

```go
// HordeConfig holds the horde: section of fabrica.yaml.
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
```

`AmiID` has no default. `NewCreatePlan` returns an error immediately if it is empty:

```
horde.amiId is required. Provide an AMI ID that contains MongoDB, Redis,
and the Horde server. See: https://github.com/jpvelasco/fabrica/blob/main/docs/horde-ami.md
```

All other fields have defaults applied in `NewCreatePlan` (same pattern as `internal/perforce/plan.go`).

---

## Provisioning Design

### Resources Created (in order)

1. `AWS::EC2::SecurityGroup` — `fabrica-horde-sg`
2. `AWS::EC2::Instance` — `fabrica-horde`, launched from `horde.amiId`

State is written after each resource. A partial failure leaves a recoverable record; re-running `create` detects the already-provisioned module and exits cleanly.

### Security Group

| Port | Protocol | Default CIDR | Purpose |
|------|----------|--------------|---------|
| 5000 | TCP | `10.0.0.0/8` | Horde HTTP API + web UI |
| 5002 | TCP | `10.0.0.0/8` | Horde HTTP/2 — agent gRPC |

MongoDB (27017) and Redis (6379) are localhost-only — no ingress rules.

`HordeConfig.AllowedCIDR` applies to both port rules. If set to `0.0.0.0/0`, a warning is printed in dry-run output and post-create output:

```
WARNING: horde.allowedCidr is 0.0.0.0/0 — ports 5000 and 5002 are open
         to the internet. Restrict this in fabrica.yaml before connecting
         agents or running production workloads.
```

### Instance Sizing

| Type | vCPU | RAM | $/hr | $/mo |
|------|------|-----|------|------|
| `m7i.xlarge` (default) | 4 | 16 GiB | $0.2016 | $147.17 |
| `m7i.2xlarge` (production) | 8 | 32 GiB | $0.4032 | $294.34 |

Dry-run output and post-create output include: *"Consider m7i.2xlarge for production workloads with >10 agents."*

### cloud-init

The AMI must already contain MongoDB, Redis, and the Horde server as enabled systemd units. cloud-init handles only final configuration and service startup — no software installation, no network calls, no credentials other than the generated MongoDB password.

```bash
#!/bin/bash
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
cat > /etc/horde/Server.json <<'EOF'
{
  "Horde": {
    "DatabaseConnectionString": "mongodb://horde:{{ .MongoPassword }}@localhost:27017/Horde?authSource=admin&readPreference=primary",
    "RedisConnectionConfig": "127.0.0.1:6379",
    "HttpPort": {{ .Port }},
    "Http2Port": {{ .GRPCPort }}
  }
}
EOF

# Start services in dependency order
systemctl restart redis-server || systemctl restart redis
systemctl restart horde

touch /var/lib/cloud/instance/horde-ready
```

Template parameters: `MongoPassword`, `Port`, `GRPCPort`.

**AMI conventions** (documented in `docs/horde-ami.md`): the AMI must have `mongod`, `redis-server` (or `redis`), and `horde` as enabled systemd units, and the `horde` unit must read `/etc/horde/Server.json`. See `docs/horde-ami.md` for a build guide.

### Credentials

`create` generates one secret before any AWS call:
- **MongoDB horde user password** (24 chars, alphanumeric) — same generator as Perforce

Written to `.fabrica/horde-credentials.yaml` (mode 0600):

```yaml
# Horde coordinator credentials — keep secret
mongodb_password: "..."
```

Horde admin account is created through the web UI on first login. Fabrica does not manage that credential.

---

## status Command

Follows the Perforce status pattern exactly:

- Reads local state → queries Cloud Control for live EC2 data (instance type, private IP, EC2 state)
- TCP-probes port 5000 (Horde HTTP) to determine readiness
- Transitions `provisioning → ready` on first successful probe; writes state
- `--wait`/`-w` flag: polls every 15 seconds, 10-minute timeout
- `--json` flag: machine-readable `StatusOutput`

```go
type StatusOutput struct {
    Provisioned  bool   `json:"provisioned"`
    Status       string `json:"status"`
    InstanceID   string `json:"instanceId,omitempty"`
    SGID         string `json:"sgId,omitempty"`
    Version      string `json:"version,omitempty"`
    InstanceType string `json:"instanceType,omitempty"`
    PrivateIP    string `json:"privateIp,omitempty"`
    HordeURL     string `json:"hordeUrl,omitempty"`
    HordeGRPC    string `json:"hordeGrpc,omitempty"`
    HordeStatus  string `json:"hordeStatus,omitempty"` // "responding" | "unreachable" | "setting up"
}
```

---

## submit Command

### BuildGraph parsing (`internal/horde/buildgraph.go`)

```go
type BuildGraphJob struct {
    Name   string
    Target string
    // extended from XML as needed
}

func ParseBuildGraph(path string) (*BuildGraphJob, error)
```

Pure file I/O + XML parse. No AWS, no HTTP. Tested independently.

### HordeClient interface (`cmd/horde/submit/client.go`)

```go
type HordeClient interface {
    SubmitJob(ctx context.Context, job *horde.BuildGraphJob) (jobID string, err error)
    GetJobStatus(ctx context.Context, jobID string) (state string, err error)
}
```

Concrete implementation: resolves the coordinator URL by reading the instance identifier from state and calling `getResource` (Cloud Control `Get`) to retrieve the private IP — the same mechanism `status` uses. Authenticates via `Authorization: ServiceAccount <token>` header (token from `.fabrica/horde-credentials.yaml`). POSTs to `POST /api/v1/jobs`.

### Error messages

| Failure | Message |
|---------|---------|
| XML parse failure | `parsing BuildGraph file %s: %w` |
| Horde not provisioned | `Horde is not provisioned. Run 'fabrica horde create' first.` |
| Connection refused | `connecting to Horde at %s: connection refused. Is the coordinator running? Check: fabrica horde status` |
| HTTP 401/403 | `Horde rejected the request (auth): check admin token in .fabrica/horde-credentials.yaml` |
| `--wait` timeout | `timed out waiting for job %s to complete (60 minutes)` |

### `--wait` poll behavior

- Default: 30-second interval, 60-minute timeout
- Prints job state on each poll
- Ctrl-C exits cleanly; prints final Job ID so user can monitor in the web UI

---

## Integration with Existing Infrastructure

| Concern | Approach |
|---------|---------|
| `cloud.Provider` | Same as Perforce: `rt.Provider.Resources().Create/Get/Delete` for SG + EC2 |
| State backend | Module name `"horde"`; `ModuleState` tracks SG identifier + instance identifier |
| Cost registry | `internal/horde/cost.go` registers `m7i.*` EC2 + gp3 EBS estimators via `init()` |
| `VPCResolver` | Interface defined in `internal/horde/config.go`; AWS provider implements it |
| Config | `internal/config/config.go`: `Config.Horde any` → `Config.Horde HordeConfig` |

---

## Testing Strategy

Follows the Perforce two-package pattern for all three commands:

- `*_test.go` (`package <cmd>`) — white-box: `command.run()` directly, exercises injection seams (`readState`, `writeState`, `createResource`, `getResource`, `confirm`, `probeTCP`, `hordeClient`)
- `cobra_test.go` (`package <cmd>_test`) — black-box: `New(...) + ExecuteContext`, flag parsing, output format

Key test cases per command:

**create:** dry-run output, already-provisioned idempotency, missing AmiID error, confirmation rejection, partial failure recovery (SG created but instance fails), `0.0.0.0/0` warning

**status:** not-provisioned output, provisioning state (TCP probe fails), ready transition (TCP probe succeeds + state write), `--wait` poll loop (mock sleep + now), `--json` output

**submit:** BuildGraph parse error, Horde not provisioned, successful fire-and-forget, `--wait` poll to completion, `--wait` timeout, auth error

`internal/horde/buildgraph.go` and `internal/horde/userdata.go` are tested in their own `*_test.go` files (pure functions, no mocking needed).

Target: 60%+ coverage on `internal/horde/`, consistent with project convention.

---

## Post-create UX (final)

```
Horde coordinator provisioned.

  Instance ID:    i-0abc123
  Status:         provisioning (Horde starting up, ~3 min)

  Horde HTTP:     http://<private-ip>:5000    (web UI + REST API)
  Horde gRPC:     <private-ip>:5002           (agent connections)

  Credentials:    .fabrica/horde-credentials.yaml

  Note: Horde is accessible via the instance's private IP. Ensure your
        machine can reach it (VPN, VPC peering, or same-VPC access).
        To allow broader access, update horde.allowedCidr in fabrica.yaml.

Next steps:
  1. fabrica horde status -w       Wait for coordinator to become ready
  2. Open http://<private-ip>:5000 Complete admin account setup in the web UI
  3. fabrica horde submit <file>   Submit a BuildGraph job
```

Warning line appended when `AllowedCIDR == "0.0.0.0/0"`:
```
  WARNING: horde.allowedCidr is 0.0.0.0/0 — ports 5000 and 5002 are open
           to the internet. Restrict this in fabrica.yaml before connecting
           agents or running production workloads.
```

---

## Cost Estimate (dry-run output)

| Resource | Cost/mo | Confidence |
|----------|---------|------------|
| EC2 m7i.xlarge | $147.17 | high |
| EBS gp3 100 GiB | $8.00 | high |
| **Total** | **$155.17** | **high** |

*Consider m7i.2xlarge ($294.34/mo) for production workloads with >10 agents.*

---

## Out of Scope for PR #1

- `fabrica horde destroy` — PR #2
- Agent fleet / Auto Scaling Group — future PR
- OIDC authentication — future PR
- `fabrica horde logs`, `fabrica horde cancel` — future PR (when REST client moves to `internal/horde/client/`)
- Multi-region, load balancer, DocumentDB — not V1

---

## Implementation Notes

- `docs/horde-ami.md` is created as a stub in PR #1 (stub is sufficient; the error message must point somewhere real)
- `internal/cost/estimators_phase0.go` is not modified — `internal/horde/cost.go` registers its own estimators
- The `m7i.*` price table in `internal/horde/cost.go` mirrors the structure of `internal/perforce/cost.go` (future cleanup: factor shared EC2 price table into `internal/cost`)
- `internal/cloud/aws/cloudcontrol.go` stubs are unchanged — Horde create/destroy go through the same stubbed `Create`/`Delete` calls as Perforce does today
