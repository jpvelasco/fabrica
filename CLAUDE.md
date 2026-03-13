# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build Commands

```bash
dotnet build                                    # build all projects
dotnet test                                     # run all tests
dotnet test --filter "FullyQualifiedName~Name"  # run a single test
dotnet run --project src/Fabrica.Cli -- doctor  # run a CLI command
cdk synth                                       # synthesize CloudFormation templates
cdk synth fabrica-foundation                    # synthesize a single stack
```

CDK synthesis runs `dotnet run --project src/Fabrica.CdkApp` under the hood (configured in `cdk.json`).

## Architecture

Fabrica is a game studio infrastructure tool built on AWS CDK. Part of a three-product suite: **Ludus** (UE5 engine builds) → **Fabrica** (infrastructure) → **Classis** (fleet operations).

### Four-project solution with clear data flow

```
fabrica.yaml → Fabrica.Cli (user commands)
                    │
                    ├──→ Fabrica.CdkApp (reads CDK context, wires stacks)
                    │         │
                    │         └──→ Fabrica.Constructs (CDK L3 constructs → CloudFormation)
                    │
                    └──→ Fabrica.Operations (day-2 AWS SDK calls: status, backup, cost)
```

**Fabrica.Cli** parses `fabrica.yaml` via `FabricaConfig` (YamlDotNet), then shells out to `cdk deploy/destroy` passing config as CDK context args (`-c fabrica:project=X -c fabrica:region=Y -c fabrica:account=Z`). It also calls Operations directly for non-CDK commands like `status`.

**Fabrica.CdkApp** is the CDK entry point. It reads context from the CDK app, instantiates stacks with explicit dependency ordering (`AddDependency`), and calls `app.Synth()`. This is what CDK CLI invokes.

**Fabrica.Constructs** contains the infrastructure definitions. Two base types enforce standards:
- `FabricaStack` (extends CDK `Stack`) — base for module-level stacks. Auto-applies tags.
- `FabricaConstruct` (extends CDK `Construct`) — base for individual L3 constructs within stacks. Auto-applies tags.

Both use `ResourceName()` for consistent `{project}-{module}-{resource}` naming.

Stack dependency graph: Foundation → Perforce → BuildFarm. Cross-stack references (VPC, PrivateZone) are passed via typed props, not string lookups.

**Fabrica.Operations** provides AWS SDK clients for runtime operations that CDK doesn't cover.

### CDK construct pattern

Props that inherit from `StackProps` (via `FabricaStackProps`) must be **classes**, not records — CDK's JSII-generated types aren't records. Props for non-stack constructs (`FabricaConstructProps`) can be records.

System.CommandLine 2.0.5 API: use `SetAction(async (parseResult, cancellationToken) => ...)` not `SetHandler`. Options use `Add(option)` in constructors and `result.GetValue(option)` in handlers. Invocation: `root.Parse(args).InvokeAsync()`.

## Conventions

- C# 13, .NET 10, AWS CDK v2
- IAM via CDK grant methods only (`bucket.GrantReadWrite(role)`) — never write raw IAM policy documents with `resources = ["*"]`
- Security groups via CDK Connections API — never use CIDR blocks where SG references work
- Standard tags on all resources: `ManagedBy: fabrica`, `fabrica:Module`, `fabrica:Project`, `fabrica:Version` (enforced by base classes)
- Config: `fabrica.yaml` (gitignored). Use `fabrica.example.yaml` as template.
- V1 targets UE5 only. Multi-engine support (Unity, Godot) planned via chassis adapter pattern in V2.
