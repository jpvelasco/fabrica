# Changelog

All notable changes to Fabrica are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.6] - 2026-08-05

### Fixed

- **CodeBuild VPC IAM permissions** — `ci setup` now grants the full set of EC2 permissions the CodeBuild service role needs for VPC networking: ENI lifecycle (`CreateNetworkInterface`, `DeleteNetworkInterface`, `CreateNetworkInterfacePermission`), VPC describe actions (`DescribeDhcpOptions`, `DescribeNetworkInterfaces`, `DescribeSecurityGroups`, `DescribeSubnets`, `DescribeTags`, `DescribeVpcs`), and tagging (`CreateTags`, `DeleteTags`). Without these, builds fail during PROVISIONING with `VPC_CLIENT_ERROR: UnauthorizedOperation`. (#216, #218)

### Note

CI projects now attach to a VPC so builds can reach a private-IP Horde coordinator. The CodeBuild service role includes the full ENI permission set required for VPC networking. Submit and trigger commands report a clear error when the coordinator has no job-creation API. Job execution still requires a Horde AMI that exposes the jobs surface — Fabrica does not build or provide this AMI.

## [0.1.5] - 2026-08-04

### Fixed

- **CodeBuild project VPC placement** — `ci setup` now resolves the default VPC (or `ci.vpcId`/`ci.subnetId` from `fabrica.yaml`) and attaches the CodeBuild project to it via a dedicated security group so builds can reach a private-IP Horde coordinator. Implements the `cloud.VPCResolver` interface on the AWS provider. (#213)
- **Horde submit 404 diagnosis** — `horde submit` now probes `GET /api/v1/jobs` on a 404 response and, when that route is provably absent, reports a clear contract-mismatch error with guidance instead of a misleading status code. The CI buildspec pre-flights the route too. (#214)

### Changed

- **CI workflow** — bumped `codeql-action` to 4.37.4 across all steps (init, autobuild, analyze). (#212)
- **Dependencies** — bumped AWS SDK v2 group (11 packages) and `smithy-go` to latest patch versions. (#203, #204)

## [0.1.4] - 2026-08-02

### Refactored

- **Horde cloud-init to Docker compose** — replaced systemd-based cloud-init (mongosh, systemctl) with Docker compose stack (`docker compose up -d` in `/etc/horde/`). Removes `GRPCPort` from `UserDataConfig` (compose uses fixed ports 5000/5002). MongoDB password is validated but not rendered into cloud-init — the compose stack manages credentials independently. (#207)
- **Horde AMI documentation** — full rewrite of `docs/horde-ami.md` for Docker compose AMI: requirements (Ubuntu 22.04 + Docker CE), compose stack example, config files, bake instructions, and pitfalls. README Horde section and credentials output updated for honesty about compose-managed passwords. (#208)

### Note

Existing systemd-style Horde AMIs are not compatible with this cloud-init; rebuild per the new guide in `docs/horde-ami.md`.

## [0.1.3] - 2026-08-02

Critical reliability fixes discovered during live AWS road testing of v0.1.2.

### Fixed

- **`resolveSeam` nil-deref on state backend constructors** — the generic `resolveSeam` helper always took the seam path (closure literal is non-nil) but the inner seam field was nil in production, causing a nil pointer dereference on every state-backend command (`setup`, `doctor`, `destroy --all`). Replaced with direct nil-checks on the actual seam fields across all five client constructors.
- **`StateBucketExists` missing `NotFound` AWS error code** — `HeadBucket` returns `"NotFound"` in the AWS SDK v2, not `"404"` or `"NoSuchBucket"`. `doctor` and `status` now correctly report a missing bucket instead of failing with an unrecognized error.
- **t3 instance family missing from cost estimator** — `t3.large`, `t3.xlarge`, and `t3.2xlarge` prices added to the EC2 cost estimator. `cost report` now shows accurate estimates for t3 instances instead of falling back to unknown.

## [0.1.2] - 2026-08-02

Reliability and quality release after the post-v0.1.1 tech-debt / coverage / Codacy sprint.

### Fixed

- **Partial-failure recovery on CREATE** — when Cloud Control reports `AlreadyExists`, recover the existing resource identifier from the progress event and continue instead of erroring. Closes the E2E gap where a resource was created on AWS but `WriteState` failed, causing a CREATE retry on the next run. (#107)
- **Perforce AMI resolution** — `perforce create` now resolves the latest Ubuntu 22.04 (jammy) HVM AMI in the target region and injects `ImageId` into the instance desired state (previously missing, causing create failure). (#106)
- **Codacy / Go 1.25** — drop broken `run-gosec` from the Codacy Analysis action (gosec v2.15.0 panics on modern export data); gosec remains gated by the dedicated CI job and golangci-lint. (#197)
- **npm install hardening** — path boundary checks, URL redirect allowlist, and SSRF protection in the binary install script.
- **Trivy CVE** — upgrade viper transitive deps (`golang.org/x/text` and related) to clear CRITICAL/HIGH findings.

### Changed

- Large structural deduplication across plan, cost, userdata, IAM, status, teardown, and test layers:
  - New shared packages: `internal/ec2plan`, `internal/ec2state`, `internal/ec2cost`, `internal/userdata`, `internal/iamrole`, `cmd/internal/testutil`, expanded `cmd/internal/provision` / `modstatus` / `teardown`
  - Shared create/status/teardown printers and constructors; credential formatting consolidated
  - Workstation start/stop unified under a single action command
- Test coverage raised across the board (many packages mid-to-high 90%s); white-box tests added for previously gapped packages (horde destroy, workstation terminate, etc.)
- Codacy profile locked (cloud + local mirror); CI comments document the split (CI owns golangci/gosec)
- AWS SDK and GitHub Actions dependency bumps

### Note

No new user-facing modules or commands. Phase 1 + Lore + DDC V1 remain the shipped surface.

## [0.1.1] - 2026-07-21

Bug fixes discovered during live AWS E2E testing of v0.1.0.

### Fixed

- **SG `Description` → `GroupDescription`** in Cloud Control desired-state for
  all five EC2 modules (perforce, horde, lore, ddc, workstation). The Cloud
  Control `AWS::EC2::SecurityGroup` schema requires `GroupDescription`, not
  `Description`. Without this fix, `create`/`setup` fails on the first
  resource with `InvalidParameterValue`. (#99)
- **`injectFabricaTags` skips `AWS::IAM::InstanceProfile`** — Cloud Control
  rejects `Tags` on IAM InstanceProfile resources. The denylist prevents
  tag injection for this type. (#100)
- **`IamInstanceProfile` as plain string** — the Cloud Control schema for
  `AWS::EC2::Instance` expects `IamInstanceProfile` as a string (profile name),
  not an object with a `Name` key. Perforce and DDC instance desired states
  fixed. (#101)
- **Perforce SG `allowedCidr` config field** — the Perforce security group no
  longer hardcodes `0.0.0.0/0` on port 1666. Set `perforce.allowedCidr` in
  `fabrica.yaml` to restrict access. Defaults to `10.0.0.0/8` (private network).
  Dry-run output shows the CIDR and warns when open to the internet. (#102)
- **`fabrica setup` persists backend names to config** — the resolved S3 bucket
  and DynamoDB table names are now written to `fabrica.yaml` after successful
  bootstrap. Fixes `doctor` and `status` showing "not configured" after setup.
  (#103)

## [0.1.0] - 2026-07-21

First public release of Fabrica: Phase 1 core plus Lore (v0.2), Perforce
backup/restore, and Distributed DDC V1 (single home-region).

### Added

- **Distributed DDC (Phase 2 M2 V1):** `ddc setup` / `status` / `destroy` —
  single home-region Unreal Cloud DDC (Jupiter), hybrid EBS+S3, default `zen`
  backend with optional 1-node Scylla bootstrap (not HA). Topology types for
  future multi-region; no `region add` in V1. Included in `destroy --all` and
  cost report.
- **Perforce backup / restore:** `perforce backup` / `backup list` /
  `backup delete` / `restore` — EBS-primary checkpoints via SSM, optional S3
  export, last-backup fields on `perforce status`. Create attaches an SSM
  instance profile; destroy retains the data volume (and local backups).
- **Lore module (v0.2):** `lore create` / `status` / `destroy` — AMI-first
  Epic `loreserver` on EC2 (local/EBS store); SG opens TCP+UDP 41337 and TCP
  41339; status probes `GET /health_check`. Parallel to Perforce (both coexist).
- **Foundation:** `fabrica setup` (S3 + DynamoDB state backend, idempotent),
  `fabrica status` (aggregate read-only health across modules, `--probe`),
  `fabrica doctor` (prerequisite validation), `fabrica config show`.
- **Perforce module:** `perforce create` / `status` / `destroy` / `backup` /
  `restore` — provisions Perforce Helix Core on EC2 with day-2 backup/restore.
- **Horde module:** `horde create` / `status` / `submit` / `destroy` /
  `ami build` — Unreal Horde build coordinator + BuildGraph job submission.
- **Workstation module:** `workstation create` / `list` / `stop` / `start` /
  `terminate` — NICE DCV cloud workstations.
- **CI module:** `ci setup` / `trigger` / `status` / `logs` / `destroy` —
  CodeBuild orchestration over Horde.
- **Deploy module:** `deploy setup` / `promote` / `rollback` / `status` /
  `destroy` — GameLift blue/green deployment.
- **Cost module:** `cost report` / `forecast` / `alerts` — offline,
  config-derived cost visibility and local budget guardrails.
- **Full-stack teardown:** `fabrica destroy --all` — ordered teardown of all
  modules then the state backend, backend removed only on full success.
- **Distribution:** cross-platform binaries via GoReleaser; npm package
  installs the matching binary.
- **Open-source project metadata:** MIT `LICENSE`, `CONTRIBUTING.md`,
  Contributor Covenant `CODE_OF_CONDUCT.md`, and `SECURITY.md`.

### Changed

- README Getting Started reworked around foundation → ddc → horde → deploy;
  status table includes `ddc` and accurate Perforce command surface; badges
  no longer use placeholder Codecov tokens.

[Unreleased]: https://github.com/jpvelasco/fabrica/compare/v0.1.6...HEAD
[0.1.6]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.6
[0.1.5]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.5
[0.1.4]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.4
[0.1.3]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.3
[0.1.2]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.2
[0.1.1]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.1
[0.1.0]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.0
