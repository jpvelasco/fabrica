# Milestone 2: CI Module — Implementation Plan

> Executed under "you have the helm" authorization. Tasks are TDD, committed incrementally.

**Goal:** `fabrica ci {setup,trigger,status,logs}` as a CodeBuild-based orchestration layer over Horde.

## Global Constraints

- Go 1.25+; `internal/cloud/*` must not import `internal/state`/`internal/cost`/`cmd/*`.
- `internal/ci` is a pure plan layer (no AWS SDK).
- Two-file tests (white-box `*_test.go` + black-box `cobra_test.go`); ≥60% coverage.
- `fmt.Errorf("context: %w", err)`; messages say what + what to do. `fmt.Print*` only.
- Cost estimators registered by TypeName; never re-register EC2/EBS/S3/DynamoDB.
- Conventional commits; gofmt; golangci-lint clean.

## Task 1 — `internal/ci` plan layer

Files: `internal/ci/plan.go`, `resources.go`, `buildspec.go`, `cost.go` + tests.

- `CreatePlan{Account,Region,ProjectName,RoleName,ComputeType,Image,BuildTimeout,HordeURL,CostResources}`.
- `NewCreatePlan(cfg config.CIConfig, account, region, hordeURL string) (*CreatePlan, error)` — apply defaults
  (ProjectName `fabrica-ci`, RoleName `fabrica-ci-codebuild`, ComputeType `BUILD_GENERAL1_SMALL`,
  Image `aws/codebuild/amazonlinux2-x86_64-standard:5.0`, BuildTimeout 60).
- `RoleDesiredState(plan)` → `AWS::IAM::Role` (AssumeRolePolicyDocument for codebuild; inline policy:
  logs:CreateLogGroup/CreateLogStream/PutLogEvents, ec2:DescribeInstances).
- `ProjectDesiredState(plan, roleARN)` → `AWS::CodeBuild::Project` (Source.Type NO_SOURCE + inline BuildSpec,
  Environment.Type LINUX_CONTAINER, ComputeType, Image, EnvironmentVariables HORDE_URL/FABRICA_REGION,
  ServiceRole roleARN, TimeoutInMinutes).
- `Buildspec(plan)`/`BuildspecRaw(plan)` — YAML; build phase curls BuildGraph job to `$HORDE_URL/api/v1/jobs`.
- `cost.go`: `TypeAWSCodeBuildProject`/`TypeAWSIAMRole`; estimators (IAM $0 high; CodeBuild low ~usage-based).

## Task 2 — `cloud.CodeBuildRunner` + AWS impl

Files: `internal/cloud/codebuild.go`, `internal/cloud/aws/codebuild.go` + test; assertion in `aws.go`.

- Interface: `StartBuild(ctx, project, env) (id,err)`, `BuildStatus(ctx,id) (BuildInfo,err)`, `BuildLog(ctx,id) (string,err)`.
- `BuildInfo{ID,Status,Phase,LogGroup,LogStream}`.
- AWS impl: seam-injected `codebuild` + `cloudwatchlogs` clients (mirror state_backend.go factory seams).
  `var _ fabricac.CodeBuildRunner = (*awsProvider)(nil)`.

## Task 3 — `cmd/ci setup`

Files: `cmd/ci/ci.go` (parent), `cmd/ci/setup/setup.go` + two-file tests.

- Resolve Horde URL from state (optional at setup; only needed at trigger). Plan → dry-run (plan+cost) →
  confirm → create role, then project (state written after each, idempotent skip on existing).
- Seams: readState/writeState/createResource/getResource/confirm.

## Task 4 — `cmd/ci trigger|status|logs`

Files: `cmd/ci/trigger/`, `cmd/ci/status/`, `cmd/ci/logs/` + two-file tests.

- trigger `<buildgraph>`: parse via `internal/horde/buildgraph`; read ci+horde state; resolve Horde private IP
  (reuse pattern from horde submit); `StartBuild(project, {BUILDGRAPH,TARGET,HORDE_URL})`; `--wait` polls BuildStatus.
- status: ci state + live BuildStatus of last build (tracked in state or via flag); `--json`.
- logs `<build-id>`: `BuildLog` → print.
- CodeBuildRunner reached via type assertion; seam `runner` in tests.

## Task 5 — config, wiring, integration test, docs

- `config.CIConfig` replaces `CI any`; defaults; fileConfig wiring.
- Register `ci.New(...)` in `cmd/root/root.go`.
- `internal/cloud/aws/codebuild_integration_test.go` (`//go:build integration`): create role+project, assert, cleanup.
- ROADMAP/CLAUDE/README/fabrica.example.yaml.

## Task 6 — verify + PR + merge

- gofmt/vet/build/test/lint; coverage check; eyeball `ci setup --dry-run`, `ci --help`.
- Push, PR, flip public → green CI → flip private → squash-merge.
