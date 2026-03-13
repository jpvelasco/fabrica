using Amazon.CDK;
using Amazon.CDK.AWS.EC2;
using Constructs;
using Fabrica.Constructs.Shared;

namespace Fabrica.Constructs.BuildFarm;

public class BuildFarmStackProps : FabricaStackProps
{
    public required IVpc Vpc { get; set; }
    public string CoordinatorInstanceType { get; set; } = "c6i.xlarge";
    public string WorkerInstanceType { get; set; } = "c6i.4xlarge";
    public double MinWorkers { get; set; } = 0;
    public double MaxWorkers { get; set; } = 4;
}

public class BuildFarmStack : FabricaStack
{
    public BuildFarmStack(Construct scope, string id, BuildFarmStackProps props) : base(scope, id, props)
    {
        // TODO: Phase 2 — BuildGraph coordinator + auto-scaling worker fleet
        // Coordinator receives BuildGraph XML, distributes tasks to workers
        // Workers pull UE5 container images from ECR (pushed by `ludus engine push`)

        new CfnOutput(this, "Status", new CfnOutputProps
        {
            Value = "placeholder",
            Description = "Build farm stack — implementation in Phase 2",
        });
    }
}
