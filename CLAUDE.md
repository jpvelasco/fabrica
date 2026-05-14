# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Status

Pre-implementation. The product spec (`Fabrica_PRODUCT_SPEC.md`) is complete; no code exists yet. Phase 0 (CLI skeleton + AWS foundation) is the current focus.

## What Fabrica Is

A Go CLI + infrastructure-as-code framework that provisions and manages game studio cloud infrastructure on AWS: Perforce Helix Core, Unreal Horde build farms, CI/CD, GameLift deployment, and cloud workstations. Sister tool to [Ludus](https://github.com/jpvelasco/ludus) — while Ludus handles a single developer's pipeline, Fabrica scales that to a full studio.

## Architecture Decisions (Already Made)

**Language/stack:** Go + Cobra + Viper — same as Ludus.

**IaC approach:** `aws-sdk-go-v2/service/cloudcontrol` (AWS Cloud Control API). No Terraform, no Pulumi, no external binaries required. Single binary. Rationale: zero prerequisite friction, 1100+ resource types including modern GameLift resources not in Terraform provider.

**State backend:**
- Remote: S3 (`s3://fabrica-state-<account-id>/`, SSE-KMS, versioned) + DynamoDB (`fabrica-state-lock`) for distributed locking
- Local cache: `.fabrica/state.json` + `.fabrica/config.yaml`
- Bootstrap: `fabrica setup` creates the S3 bucket and DynamoDB table via direct AWS SDK (not Cloud Control — bootstrapping the state store itself)

**Resource management pattern:**
```go
CreateResource(TypeName, DesiredState)
GetResource(TypeName, Identifier)
UpdateResource(TypeName, Identifier, PatchDocument)  // JSON Patch
DeleteResource(TypeName, Identifier)
ListResources(TypeName)
```
Orchestration layer handles: topological dependency resolution, plan/diff preview, rollback on partial failure, and tagging (`ManagedBy: fabrica` on all resources).

## Planned Command Structure

```
fabrica setup                          # guided first-run provisioning wizard
fabrica status                         # health of all modules
fabrica perforce [setup|status|backup|restore]
fabrica horde [setup|status|scale|workers]
fabrica ci [setup|trigger|status|logs]
fabrica deploy [setup|promote|status|destroy]
fabrica workstation [create|list|stop|terminate]
fabrica cost [report|forecast|alerts]
fabrica doctor                         # prerequisite validation
fabrica destroy --all                  # clean teardown
fabrica export --format cloudformation # escape hatch
```

## V1 Scope

In scope: CLI skeleton, `fabrica setup` wizard, Perforce Helix Core (single-server + S3 backup), Horde build farm (coordinator + auto-scaling workers), BuildGraph XML ingestion from Ludus, cost estimation before provisioning, `fabrica status`, `fabrica doctor`, `fabrica destroy`.

Out of scope for V1: workstations (NICE DCV), multi-region, CI/CD pipeline, web dashboard, multi-cloud, MCP server.

## Ludus Integration

Ludus's `ludus buildgraph` generates BuildGraph XML → Fabrica's Horde grid consumes it. Fabrica provisions deployment infrastructure → Ludus manages containers and GameLift fleets within it. Both tools share: YAML config conventions, `ManagedBy` tagging patterns, similar CLI UX patterns.

## Build Commands

_To be documented once Phase 0 scaffolding is complete. Expected:_
```
go build ./...
go test ./...
go test ./... -run TestName
golangci-lint run
```

## Open Questions (Unresolved)

- Horde vs. simpler custom job distribution for V1
- Perforce licensing strategy for teams > 5 users
- `fabrica.yaml` / `ludus.yaml` config sharing approach
- Open source vs. commercial vs. open-core pricing model
