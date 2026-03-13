using Amazon.CDK;
using Amazon.CDK.AWS.EC2;
using Amazon.CDK.AWS.Route53;
using Amazon.CDK.AWS.S3;
using Constructs;
using Fabrica.Constructs.Shared;

namespace Fabrica.Constructs.Perforce;

public class PerforceStackProps : FabricaStackProps
{
    public required IVpc Vpc { get; set; }
    public required IPrivateHostedZone PrivateZone { get; set; }
    public string InstanceType { get; set; } = "c6i.large";
    public double DepotVolumeSize { get; set; } = 256;
    public double MetadataVolumeSize { get; set; } = 64;
    public double LogsVolumeSize { get; set; } = 32;
}

public class PerforceStack : FabricaStack
{
    public PerforceServer Server { get; }
    public IBucket BackupBucket { get; }

    public PerforceStack(Construct scope, string id, PerforceStackProps props) : base(scope, id, props)
    {
        // Backup bucket with lifecycle policy
        BackupBucket = new Bucket(this, "BackupBucket", new BucketProps
        {
            BucketName = ResourceName("p4-backups"),
            Encryption = BucketEncryption.S3_MANAGED,
            BlockPublicAccess = BlockPublicAccess.BLOCK_ALL,
            RemovalPolicy = RemovalPolicy.RETAIN,
            LifecycleRules =
            [
                new LifecycleRule
                {
                    Id = "archive-old-backups",
                    Transitions =
                    [
                        new Transition
                        {
                            StorageClass = StorageClass.GLACIER,
                            TransitionAfter = Duration.Days(90),
                        },
                    ],
                    Expiration = Duration.Days(365),
                },
            ],
        });

        // Perforce server construct
        Server = new PerforceServer(this, "Server", new PerforceServerProps
        {
            ProjectName = props.ProjectName,
            ModuleName = props.ModuleName,
            Vpc = props.Vpc,
            PrivateZone = props.PrivateZone,
            InstanceType = props.InstanceType,
            DepotVolumeSize = props.DepotVolumeSize,
            MetadataVolumeSize = props.MetadataVolumeSize,
            LogsVolumeSize = props.LogsVolumeSize,
        });

        // Grant backup bucket access to the server role
        BackupBucket.GrantReadWrite(Server.Instance.Role);

        // Outputs
        new CfnOutput(this, "PerforceEndpoint", new CfnOutputProps
        {
            Value = $"perforce.{props.PrivateZone.ZoneName}:1666",
            Description = "Perforce Helix Core connection string",
        });
        new CfnOutput(this, "AdminSecretArn", new CfnOutputProps
        {
            Value = Server.AdminSecret.SecretArn,
            Description = "ARN of the Perforce admin password secret",
        });
    }
}
