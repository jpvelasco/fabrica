# Changelog

All notable changes to Fabrica are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

The first tagged release will cover the Phase 1 core: the foundation plus six
provisioning/management modules, full-stack teardown, and cost visibility.

### Added

- **Foundation:** `fabrica setup` (S3 + DynamoDB state backend, idempotent),
  `fabrica status` (aggregate read-only health across modules, `--probe`),
  `fabrica doctor` (prerequisite validation), `fabrica config show`.
- **Perforce module:** `perforce create` / `status` / `destroy` — provisions
  Perforce Helix Core on EC2.
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

[Unreleased]: https://github.com/jpvelasco/fabrica/commits/main
