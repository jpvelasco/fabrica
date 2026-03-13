using Amazon.CDK;
using Fabrica.Constructs.Foundation;
using Fabrica.Constructs.Perforce;
using Fabrica.Constructs.BuildFarm;
using Fabrica.Constructs.Shared;

var app = new App();

// Read project config from CDK context (passed by Fabrica CLI)
var projectName = (string)app.Node.TryGetContext("fabrica:project") ?? "fabrica";
var region = (string?)app.Node.TryGetContext("fabrica:region");
var account = (string?)app.Node.TryGetContext("fabrica:account");

// Only set Env when both account and region are provided; otherwise CDK uses environment-agnostic synthesis
var env = !string.IsNullOrEmpty(account) && !string.IsNullOrEmpty(region)
    ? new Amazon.CDK.Environment { Region = region, Account = account }
    : null;

// Foundation stack — VPC, subnets, NAT, DNS
var foundation = new FoundationStack(app, Naming.Stack(projectName, "foundation"), new FoundationStackProps
{
    ProjectName = projectName,
    ModuleName = "foundation",
    Env = env,
});

// Perforce stack — depends on Foundation
var perforce = new PerforceStack(app, Naming.Stack(projectName, "perforce"), new PerforceStackProps
{
    ProjectName = projectName,
    ModuleName = "perforce",
    Env = env,
    Vpc = foundation.Vpc,
    PrivateZone = foundation.PrivateZone,
});
perforce.AddDependency(foundation);

// Build Farm stack — depends on Foundation
var buildFarm = new BuildFarmStack(app, Naming.Stack(projectName, "build-farm"), new BuildFarmStackProps
{
    ProjectName = projectName,
    ModuleName = "build-farm",
    Env = env,
    Vpc = foundation.Vpc,
});
buildFarm.AddDependency(foundation);

app.Synth();
