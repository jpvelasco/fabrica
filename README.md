# Fabrica

*Game studio infrastructure on AWS — zero assembly required.*

Fabrica provisions and manages the cloud infrastructure a game studio needs: Perforce source control, distributed build farms, CI/CD, and cloud workstations. One command, under an hour, fully configured.

Part of the product suite: **Ludus** (engine builds) → **Fabrica** (infrastructure) → **Classis** (fleet operations).

## Prerequisites

| Dependency | Version | Install |
|------------|---------|---------|
| .NET SDK | 10.0+ | https://dotnet.microsoft.com/download |
| Node.js | 20+ | https://nodejs.org/ |
| AWS CDK CLI | 2.x | `npm install -g aws-cdk` |
| AWS CLI | 2.x | https://aws.amazon.com/cli/ |
| AWS credentials | — | `aws configure` or environment variables |

Verify with:

```bash
fabrica doctor
```

## Quick Start

```bash
# Clone and build
git clone https://github.com/jpvelasco/fabrica.git
cd fabrica
dotnet build

# Configure
cp fabrica.example.yaml fabrica.yaml
# Edit fabrica.yaml with your project name, region, and module preferences

# Check prerequisites
dotnet run --project src/Fabrica.Cli -- doctor

# Deploy infrastructure
dotnet run --project src/Fabrica.Cli -- setup

# Check status
dotnet run --project src/Fabrica.Cli -- status

# Tear down
dotnet run --project src/Fabrica.Cli -- destroy --all
```

## Project Structure

```
fabrica/
├── src/
│   ├── Fabrica.Cli/           # CLI commands (setup, status, doctor, destroy)
│   ├── Fabrica.Constructs/    # CDK L3 constructs (Foundation, Perforce, BuildFarm)
│   ├── Fabrica.Operations/    # Day-2 operations via AWS SDK
│   └── Fabrica.CdkApp/       # CDK app entry point
├── tests/
│   └── Fabrica.Tests/
├── cdk.json                   # CDK configuration
├── fabrica.example.yaml       # Example configuration
└── PRODUCT_SPEC.md            # Full product specification
```

## License

TBD
