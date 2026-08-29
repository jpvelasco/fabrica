# Changelog

All notable changes to Fabrica are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **Export emits the inline IAM policies create actually attaches** — `fabrica export` printed only `ManagedPolicyArns` on `AWS::IAM::Role`, so CloudFormation and Terraform output omitted the inline policies `iamrole.RoleDocument` sends to Cloud Control (the shared `fabrica-ssm-output` on perforce, horde coordinator, horde agents, ddc, and lore, plus the Lore store S3/DynamoDB and DDC/Perforce-Backup S3 bucket policies). Both formats now re-derive the same desired-state `Policies` from the shared helpers (no second copy of the policy text): CloudFormation carries them on the role `Policies`, Terraform on the `aws_iam_role` `inline_policy` map. Secrets stay redacted.
- **Export logical-ID collision** — `toLogicalID` truncated the sanitized identifier to 12 chars, so `fabrica-horde-role` and `fabrica-horde-agents-role` (and their instance profiles/SGs) both mapped to `HORDERoleFABRICAHORDE` / `HORDEInstanceProfileFABRICAHORDE` / `HORDESecurityGroupFABRICAHORDE`, and a state file containing coordinator + agents produced an invalid template with duplicate keys. The generator now sanitizes to alphanumerics only, caps at 32 chars, and appends an 8-char FNV-1a hash of the original identifier so every distinct name yields a unique, stable, valid CloudFormation logical ID (e.g. `HORDERoleFABRICAHORDEROLE30103C97` vs `HORDERoleFABRICAHORDEAGENTSROLE48804154`); instance profiles and SGs are covered by the same fix. Golden fixtures that assumed the 12-char truncate have been updated.
- **CI and Deploy inline policies now shared via `internal/iamrole`** — `fabrica-ci-inline` (CloudWatch Logs scoped to `log-group:/aws/codebuild/<project>*` plus the 12 EC2/VPC actions `CreateNetworkInterface`, `CreateNetworkInterfacePermission`, `DeleteNetworkInterface`, `DescribeDhcpOptions`, `DescribeInstances`, `DescribeNetworkInterfaces`, `DescribeSecurityGroups`, `DescribeSubnets`, `DescribeTags`, `DescribeVpcs`, `CreateTags`, `DeleteTags` that the live CodeBuild role actually has) and `fabrica-deploy-s3-read` (`s3:GetObject` on `arn:aws:s3:::<buildBucket>/*`) were hand-built JSON in `internal/ci`/`internal/deploy` and omitted from `export`. Both now live as `iamrole.CICodeBuildInlinePolicy` / `iamrole.DeployS3ReadPolicy` (same family as `SSMOutputPolicy` / `S3BucketPolicy`); `RoleDesiredState` and `export.inlinePoliciesForRole` re-derive the same document — one document, two renderers (CFN `Policies` / TF `inline_policy`) — and no second copy of the policy text remains. `Grep` for the policy body hits only `internal/iamrole`. Secrets stay redacted.
- **CI/Deploy IAM slop** — `internal/ci/buildspec.go` now contains only buildspec; the wrapper that built the CI inline policy in the buildspec file (IAM in a buildspec file, `json.Marshal` error discarded) is deleted and `internal/ci`/`internal/deploy` `RoleDesiredState` now use `iamrole.RoleDocument` (`ServiceCodeBuild`/`ServiceGameLift`) like every other module role envelope — no hand-rolled `RoleName`/`AssumeRolePolicyDocument`/`Policies`/`Tags` map remains. `git grep` for the former wrapper name is empty.

### Added

- **Lore S3 store now provisions the full 0.8.6 store surface** - with `lore.storeBackend: s3`, `fabrica lore create` now provisions the four DynamoDB tables the 0.8.6 `aws` store plugin requires (`<bucket>-fragments`, `<bucket>-metadata`, `<bucket>-mutable`, `<bucket>-locks`, with the locks table's three global secondary indexes) in addition to the versioned store bucket, and grants the instance role DynamoDB permissions (`GetItem`, `PutItem`, `DeleteItem`, `Query`, `BatchGetItem`, `DescribeTable`, `TransactWriteItems` on the four tables + the locks table's GSIs) on top of the existing S3 policy. Cloud-init renders the 0.8.6 `[plugins.aws.*]` config (`mode = "aws"`, `s3_bucket` + table names). Destroy tears the tables down after the instance and with the bucket purge; export emits the tables as distinct logical IDs with per-table name outputs and full key/GSI schemas; drift checks table existence. Cost estimation includes the four tables. `storeBackend: local` is unchanged (no bucket, no tables, no instance profile).
- **Lore SSM command output sink** - the S3-store instance role now carries a least-privilege `fabrica-ssm-output` inline policy granting `ssm:PutParameter`/`GetParameter`/`DescribeParameters` on the `MDS-*` parameter and `logs:CreateLogGroup`/`CreateLogStream`/`PutLogEvents` on the `/fabrica/ssm/*` log group. This makes SSM command output retrievable for accounts whose `AmazonSSMManagedInstanceCore` is a narrowed variant (missing `ssm:PutParameter` and `logs:*`), where the CloudWatch Logs sink is the reliable retrieval path.

### Changed

- **Shared SSM output policy for all SSM-managed instance roles** - the least-privilege `fabrica-ssm-output` inline policy (MDS parameter + `/fabrica/ssm/*` CloudWatch Logs sink) now lives in `internal/iamrole` and is attached uniformly to every Fabrica-managed instance role that uses SSM: perforce, horde coordinator, horde agents, lore, and ddc. Previously only the Lore S3-store role carried it; the shared helper also dedupes the Perforce role builder onto the existing `iamrole.RoleDocument`/`S3BucketPolicy` helpers.
- **Lore AMI build bakes from the pinned GitHub release** - the Image Builder component now downloads the pinned `loreserver` release tarball directly (no staging bucket or `REPLACE_WITH_YOUR_BUCKET` placeholder), normalizes the 0644 tarball mode, and enables the SSM agent best-effort so bakes on base images without `amazon-ssm-agent.service` do not abort. Recipe emits `supportedOsVersions`. Known-good row recorded for lore v0.8.6 in us-west-2 (AMI `ami-0cb86d7ebcd1a4487`, base `ami-0bdb09211df876db4`), SSM-verified boot with health 200 on the local store.
- **Lore S3 store config format updated for 0.8.6** - cloud-init now emits the 0.8.6 `config-aws.toml` shape: `[immutable_store]`/`[mutable_store]`/`[lock_store]` with `mode = "aws"`, and `[plugins.aws.immutable_store]` (`s3_bucket`, `dynamodb_fragments_table`, `dynamodb_metadata_table`), `[plugins.aws.mutable_store]` (`dynamodb_table`), `[plugins.aws.lock_store]` (`dynamodb_table`). The local-store config uses the 0.8.6 `[immutable_store.local]` subtable form with `path`.

## [0.4.3] - 2026-08-23

### Fixed

- **Distributed state locking enforced end-to-end** — every state-mutating flow now acquires the account-level DynamoDB lock via `provision.AcquireStateLock`: teardown engine, setup, deploy promote/rollback, horde agents create/destroy, perforce backup create/delete, and `destroy --all`. LockStore has a 15-minute TTL with stale takeover; nested orchestration inherits the lock through a ctx sentinel. Commands on an unbootstrapped account proceed unlocked (`ErrLockTableMissing`). Lock release references the reserved keyword via expression attribute names so rows actually delete. (#336, #337, #338)
- **Perforce 2026.1 p4-server packaging support** — userdata detects legacy helix-p4d vs p4-server generation at runtime (configure flags + p4dctl supervisor), falls back to the current repo version when a pin rots out of the jammy archive, auto-detects the largest non-root unformatted NVMe volume as the data device (enumeration order is not deterministic), and self-installs the AWS CLI plus authenticates backups via a `p4 login -a` ticket in an isolated `P4TICKETS` file. `DefaultHelixVersion` bumped to 2025.2. (#338)
- **Volume tagging and module attribution** — BlockDeviceMapping data volumes are tagged post-create via `cloud.VolumeTagger`; `FabricaModule` is stamped on perforce, horde coordinator, and workstation desired states; `awsProvider` delegates `TagInstanceVolumes` so capability asserts succeed. (#334, #335)
- **Machine-readable `--json` output** — agents destroy, perforce backup create, `status --wait`, and `destroy --all` emit exactly one JSON document on stdout on every exit path. (#322, #333)
- **Horde agent enrollment and lore teardown** — agent SG direction corrected (standalone ingress rule on the coordinator SG sourced from the agent SG); lore S3 store bucket purged (versions + delete markers) before deletion, tolerating missing buckets; horde status restored correctly after agents destroy; all Horde API requests bounded by a 30-second timeout. (#319, #325, #327, #333)
- **State-integrity repairs** — VPC resolver wired into perforce/lore/workstation create so omitted config resolves the default VPC; partial `vpcId`/`subnetId` pairs fail fast; deploy setup preserves build/fleet records on re-setup; deploy promote verifies incremental state writes before fleet creation. (#316, #319)
- **Workstation sizing and cost fidelity** — per-field precedence (flags > config > template > default), cost estimates price the resolved shape, and g6.xlarge/g6.2xlarge/g6.4xlarge/i4i.large prices added. (#317)
- **Export correctness** — module-internal properties no longer leak into CloudFormation/Terraform templates; fallback instance types reference real defaults; `export --dry-run` honored as documented. (#320)
- **Context-aware polling** — Ctrl+C ends submit/promote/trigger/status waits immediately; DDC edge probes thread the command context. (#326)
- **CI logs and backups** — `ci logs` paginates CloudWatch Logs past ~10k events instead of truncating; generated backup scripts write metadata manifests alongside uploads. (#323)
- **Road-test batch** — drift no longer flags a deliberately stopped workstation; state-backend waiter contexts bound at 3 minutes; cost-alert scope list derived from known scopes; mutex-guarded lazy client init closes the MCP concurrency race; explicit `--min-size 0` wins over nonzero config. (#324)

### Changed

- **Go toolchain 1.25.13** — clears five reachable stdlib vulnerabilities reported by govulncheck on 1.25.12. (#318)
- **Docs** — AGENTS.md refreshed to current architecture, locking semantics, and live road-test knowledge (provider capability delegates, bootstrap ordering, Perforce packaging generations); setup warns that it rewrites `fabrica.yaml` via Viper. (#328, #340, #341)

## [0.4.2] - 2026-08-13

### Added

- **Lore S3 store backend** — `fabrica lore create` now supports an opt-in S3 store backend (`lore.storeBackend: "s3"`). When enabled, provisions an S3 bucket (versioning + encryption + public-access-block), IAM role (SSM + S3 bucket access), and instance profile before the EC2 instance. Cloud-init renders S3-based `local.toml` with bucket/prefix configuration. Default remains "local" (EBS-only). Cost estimation includes the S3 bucket. Destroy order reversed for S3 resources. Export and drift aware of S3 buckets and IAM instance profiles. (#274)
- **Lore AMI build command** — `fabrica lore ami build` generates Image Builder artifacts (component + recipe) for building a Lore AMI, with optional Packer template. Produces `component.yaml`, `image-builder-recipe.json`, `build-guide.md`, and optionally `packer.pkr.hcl`. No AWS calls — all output is local. Flags: `--lore-version`, `--base-image`, `--region`, `--output-dir`, `--include-packer`, `--dry-run`.
- **Lore AMI runbook** — `docs/lore-ami.md` rewritten as a comprehensive executable build guide: requirements table, two build methods (native install recommended, Docker alternative), Packer template example, cloud-init interaction details, verification checklist with script, known-good AMI table, and common pitfalls.
- **Lore TLS foundation** — `lore.tls` config section (`LoreTLSConfig` with `Enabled`, `CertPath`, `KeyPath`) added to `fabrica.yaml`. Config fields are parsed but not yet wired into cloud-init (no-op until V2). Design note at `docs/lore-tls.md`. (#276)
- **Queue-based autoscaling for Horde agent pool** — `fabrica horde agents create --scaling-enabled` provisions two CloudWatch alarms and two SimpleScaling policies that drive the ASG capacity based on an external metric (default `ASGQueueDepth`). Flags: `--scale-out-threshold`, `--scale-in-threshold`, `--scale-in-cooldown`. `horde agents status` reports scaling policy and live ASG lifecycle data. Destroy order includes scaling policies and alarms before the ASG. **Note:** Queue scaling requires agents to publish the configured metric to CloudWatch — Fabrica provisions the alarms and policies but does not publish the metric itself. (#279, #280)
- **Horde agents status external-metric warning** — When scaling is enabled, status output now includes a note reminding operators that the metric must be published externally.

### Fixed

- **Horde autoscaling bugs** — four fixes in queue-based scaling: tag denylist for ScalingPolicy/CloudWatchAlarm (prevents Cloud Control validation rejection), stringified `Cooldown` property in scaling policy desired state, corrected `PutMetricData` example with matching Dimensions, and proper state storage of PolicyName/AutoScalingGroupName/AlarmName for export fidelity. (#279)
- **Horde destroy order** — added ScalingPolicy and CloudWatchAlarm deletion phases before ASG teardown, preventing orphaned scaling resources. (#279)
- **Ops logging coverage** — added `oplog.Debug` calls to horde agents create (scaling resources), horde agents destroy (per-resource deletion), and lore create (S3 store resources). (#280)

### Changed

- **Export Lore IAM roles** — Lore IAM roles now include `AmazonSSMManagedInstanceCore` in exported IaC templates via `managedPolicyARNsForModule`. (#276)
- **Export/Drift S3 coverage** — Lore S3 store resources (bucket, IAM role, instance profile) covered in export and drift checks. (#276)

## [0.4.1] - 2026-08-12

### Added

- **Dedicated agent AMI runbook** — `docs/horde-agent-ami.md` rewritten as an executable build guide: prerequisites (UE/Horde agent source), two build methods (native install recommended, Docker container as trial mode), Packer template example, cloud-init interaction details, verification checklist with script, known-good AMI table, and common pitfalls. Clarifies that `horde.agents.amiId` is independent from `horde.amiId` and must not be the coordinator AMI. (#272)
- **Agent UserData tests** — extended `internal/horde/agents_userdata_test.go` with tests for agent-only service name (`horde-agent`), correct config path (`/etc/horde/coordinator.conf`), INI format, no secrets in output, environment variable injection, no full compose stack, readiness sentinel, and custom port support. (#272)

### Changed

- **CLI agent AMI distinction** — `horde ami build` Long text now clarifies that the agent AMI is distinct from the coordinator AMI and targets `horde-agent.service` only. (#272)

## [0.4.0] - 2026-08-11

### Added

- **Horde agents V1** — `fabrica horde agents create|status|destroy` provisions a managed agent pool on AWS. Creates an Auto Scaling Group with a Launch Template that launches agent instances in private subnets. Agents enroll against the existing Horde coordinator via private IP. Resources: agent SG (coordinator SG source, no internet inbound), IAM role (SSM only), instance profile, launch template, ASG. Config: `horde.agents.amiId`, `instanceType`, `minSize`, `desiredCapacity`, `maxSize`. CLI flags override config. `--dry-run` shows plan + cost estimate. (#265)
- **Drift ASG exclusion** — `fabrica drift` now excludes ASG-managed instances (tagged `FabricaRole=agent`) from Extra resource detection, preventing false positives for dynamically launched agent instances. (#265)
- **Horde destroy includes agents** — `fabrica horde destroy` and `fabrica destroy --all` now tear down agent resources (ASG → LT → IAM → SG) before the coordinator instance, preventing orphaned resources. (#265)
- **Export ASG + LaunchTemplate** — `fabrica export` now includes Auto Scaling Group and Launch Template resources when the horde module contains agent resources. (#265)
- **Agent AMI documentation** — `docs/horde-agent-ami.md` documents dedicated agent AMI requirements for production use.

### Fixed

- **c7i instance prices** — added c7i.xlarge, c7i.2xlarge, and c7i.4xlarge on-demand prices for agent cost estimation. `horde agents create --dry-run` now shows accurate cost estimates instead of `$0.00`. (#267)
- **Cloud Control waiter StatusMessage** — when a Cloud Control operation fails, the waiter now surfaces the actual StatusMessage from the ProgressEvent instead of the generic "waiter state transitioned to Failure" message. (#268)

## [0.3.5] - 2026-08-10

### Added

- **DDC live edge probes** — `fabrica ddc status` now probes edge regions live via region-scoped Cloud Control queries and optional HTTP `/health/ready` health probes. Edge status reports `ready`, `unreachable`, `stopped`, `terminated`, or `missing` per region. Operators outside the VPC get graceful `unreachable`/`missing` states without command failure. (#260)

### Fixed

- **Perforce state AMI fidelity** — `fabrica perforce create` now records the resolved AMI ID (`ami-…`) in instance `Properties.imageId` instead of the Helix version string (`2024.2`). This fixes false `fabrica drift` mismatches (version vs AMI) and ensures `fabrica export` emits the correct AMI in CloudFormation/Terraform output. The Helix version string is preserved in `ModuleState.Version` for human-readable status display. Drift comparison reads `Properties.imageId` first, falling back to `ModuleState.Version` for backward compatibility with Horde/Lore/DDC. (#264)

## [0.3.4] - 2026-08-10

### Added

- **Export V2** — `fabrica export` now covers all modules: DDC (home + edge regions, S3 bucket, IAM role/profile), Workstation (SG + EC2), CI (IAM role, CodeBuild project), and Deploy (IAM role, GameLift alias/fleet/build). Metadata version updated to "v2". (#255)

### Fixed

- **CI destroy** — `ci destroy` now includes `AWS::EC2::SecurityGroup` in the deletion sequence (project → SG → role), preventing orphaned VPC security groups. (#258)
- **Workstation default CIDR** — `DefaultAllowedCIDR` changed from `0.0.0.0/0` to `10.0.0.0/8`, enforcing private-CIDR-only posture on DCV port 8443 by default. (#258)
- **Export defaults** — CodeBuild artifacts set to `NO_ARTIFACTS` to match production; workstation root device name `/dev/sda1` via `deviceNameForModule`; SG default CIDRs aligned with each module's actual create defaults via `moduleDefaultCIDR`. (#257, #258)

### Changed

- **Dependencies** — AWS SDK v2 group (11 packages) and CodeQL action (4.37.4 → 4.37.6) bumped to latest. (#247, #248, #249, #250)

## [0.3.3] - 2026-08-10

### Added

- **Ops logging** — stdlib `log/slog` via `internal/oplog`: stderr operational diagnostics for state I/O, Cloud Control errors, drift `--fix`, destroy-all milestones, and bootstrap failures. Enable with `--verbose` or `FABRICA_LOG_LEVEL=debug`. Default remains quiet; no third-party log libraries. Secrets are never logged. (#246)

### Changed

- **Docs and Init clarity** — improved documentation and initialization clarity follow-up. (#254)
- **State write cleanup** — removed unreachable `MarshalIndent` error branch in `WriteState` (behavior-preserving).

## [0.3.2] - 2026-08-08

### Changed

- **npm package README** — removed Roadmap link from npmjs.com package page. (#245)

## [0.3.1] - 2026-08-08

### Added

- **DDC multi-region edge nodes** — `fabrica ddc region add REGION` provisions a peer-region edge node (SG + AMI-first EC2) reusing the home blob bucket and IAM profile. Multiple edge regions supported; `ddc status` lists all edges from local state. `ddc destroy` tears down edges first (per region) then the home stack. `cloud.RegionProvider` interface for region-scoped clients; `internal/topology` coordinator/edge graph types. Replication peers remain operator-managed; edge status is state-based (no live edge probes in this cut). (#238)

### Changed

- **CI** — removed redundant Codacy CLI upload job; aligned local Codacy config with cloud profile. (#239)

## [0.3.0] - 2026-08-07

### Added

- **Drift detection** — `fabrica drift` compares recorded state against live AWS resources and reports whether each resource is in-sync, missing, extra (live but not in state), or has attribute mismatches. Covers state backend (S3/DynamoDB), EC2 instances (state, type, AMI), security groups, IAM roles, and CodeBuild projects. Extra detection uses `ResourceClient.List` to enumerate live resources and diff against recorded state. `--json` for machine-readable output. (#228, #230)
- **MCP server** — `fabrica mcp` runs a Model Context Protocol server over stdio transport, exposing 6 read-only tools (`fabrica_version`, `fabrica_doctor`, `fabrica_status`, `fabrica_drift`, `fabrica_cost_report`, `fabrica_config_show`). Reuses existing business logic from `internal/*` packages with zero duplication. Doctor and status logic extracted to shared `cmd/internal/doctorchecks` and `cmd/internal/statusreport` packages. (#232)
- **Export** — `fabrica export --format cloudformation|terraform` generates infrastructure-as-code templates (CloudFormation YAML or Terraform HCL) from recorded local state. V1 covers the state backend (S3 bucket, DynamoDB table) and Horde, Perforce, and Lore modules. Secrets (UserData, credential-like fields) are redacted in output. DDC, Workstation, CI, and Deploy deferred to V2. (#233)

### Fixed

- **Export edge cases** — `toLogicalID` now guards against empty identifiers (preventing panic on strings that strip to empty). S3 public access block HCL emits real property fields instead of comment-only placeholders. IAM policy document HCL reads actual Effect/Principal/Action from the policy Statement array instead of hardcoding EC2 defaults, supporting non-EC2 policies and multi-statement documents. (#234)
- **Export no-state-file behavior** — when `.fabrica/state.json` is absent, `fabrica export` now prints a warning and exits 0 instead of creating a state from config defaults. (#235)
- **Terraform output references** — HCL2 output values use bare resource references (`aws_s3_bucket.name.id`) instead of invalid HCL1 interpolation syntax (`${resource.attr}`). (#236)

## [0.2.0] - 2026-08-06

### Added

- **SSM instance profile for Horde** — `horde create` now provisions an IAM role (`AmazonSSMManagedInstanceCore`) and instance profile alongside the EC2 instance, enabling Session Manager shell access without manual IAM setup. Destroy deletes resources in correct reverse order (instance → profile → role → SG). (#222)

### Fixed

- **Horde allowedCidr auto-resolve** — `horde create` now resolves the VPC CIDR block via a new `cloud.VPCCIDRResolver` interface and defaults `allowedCidr` to the resolved VPC CIDR when config is unset. This fixes the common footgun where the default `10.0.0.0/8` blocks CodeBuild ENIs and workstations in AWS default VPCs (`172.31.0.0/16`). Explicit config always wins. (#226)
- **Empty BuildGraph fail-fast** — `ci trigger` and `horde submit` now validate the parsed BuildGraph has a non-empty target before making any AWS or network calls. Previously produced a silent PRE_BUILD failure; now fails fast with an actionable error pointing to `examples/BuildGraph.sample.xml`. A minimal sample BuildGraph is included. (#224)

### Changed

- **Horde AMI requirements** — docs now require a job-capable Horde AMI with bake-time verification: `GET /api/v1/jobs` must not return 404. Known-good AMI for UE 5.8.0 in us-west-2 documented (`ami-0764d44c38ef85362`). README and horde-ami.md updated. (#221, #225)

### Note

Horde instances now have SSM access out of the box. The security group defaults to the resolved VPC CIDR instead of `10.0.0.0/8`, fixing connectivity for AWS default VPCs. BuildGraph validation catches empty targets before any API calls.

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

[Unreleased]: https://github.com/jpvelasco/fabrica/compare/v0.4.3...HEAD
[0.4.3]: https://github.com/jpvelasco/fabrica/compare/v0.4.2...v0.4.3
[0.4.2]: https://github.com/jpvelasco/fabrica/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/jpvelasco/fabrica/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/jpvelasco/fabrica/compare/v0.3.5...v0.4.0
[0.3.5]: https://github.com/jpvelasco/fabrica/compare/v0.3.4...v0.3.5
[0.3.4]: https://github.com/jpvelasco/fabrica/compare/v0.3.3...v0.3.4
[0.3.3]: https://github.com/jpvelasco/fabrica/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/jpvelasco/fabrica/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/jpvelasco/fabrica/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/jpvelasco/fabrica/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/jpvelasco/fabrica/compare/v0.1.6...v0.2.0
[0.1.6]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.6
[0.1.5]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.5
[0.1.4]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.4
[0.1.3]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.3
[0.1.2]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.2
[0.1.1]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.1
[0.1.0]: https://github.com/jpvelasco/fabrica/releases/tag/v0.1.0
