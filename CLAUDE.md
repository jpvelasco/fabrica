# Fabrica

Game studio infrastructure on AWS. Part of the Ludus/Fabrica/Classis product suite.

## Build & Run

```bash
dotnet build                              # build all projects
dotnet test                               # run tests
dotnet run --project src/Fabrica.Cli      # run the CLI
dotnet run --project src/Fabrica.Cli -- doctor   # run a specific command
cdk synth                                 # synthesize CloudFormation templates
cdk deploy                                # deploy to AWS
```

## Architecture

- `Fabrica.Cli` — user-facing CLI (System.CommandLine + Spectre.Console)
- `Fabrica.Constructs` — CDK L3 constructs (Foundation, Perforce, BuildFarm stacks)
- `Fabrica.Operations` — day-2 operations via AWS SDK (backup, status, cost)
- `Fabrica.CdkApp` — CDK app entry point (reads context, instantiates stacks)
- `Fabrica.Tests` — xUnit tests

## Conventions

- C# 13, .NET 10, AWS CDK v2
- Base class `FabricaStack` / `FabricaConstruct` enforces tags and naming on all resources
- All resource names prefixed with project name: `{project}-{module}-{resource}`
- Standard tags: `ManagedBy: fabrica`, `fabrica:Module`, `fabrica:Project`, `fabrica:Version`
- IAM via CDK grant methods only — never write raw IAM policy documents
- Security groups via CDK Connections API — never use CIDR blocks where SG refs work
- Config file: `fabrica.yaml` (not committed — use `fabrica.example.yaml` as template)
- CDK context passed via `-c fabrica:project=X -c fabrica:region=Y -c fabrica:account=Z`
