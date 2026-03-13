using Amazon.CDK;
using Amazon.CDK.AWS.EC2;
using Amazon.CDK.AWS.Route53;
using Amazon.CDK.AWS.S3;
using Constructs;
using Fabrica.Constructs.Shared;

namespace Fabrica.Constructs.Foundation;

public class FoundationStackProps : FabricaStackProps
{
    public string VpcCidr { get; set; } = "10.0.0.0/16";
    public double MaxAzs { get; set; } = 2;
    public double NatGateways { get; set; } = 1;
    public string InternalDomain { get; set; } = "studio.internal";
}

public class FoundationStack : FabricaStack
{
    public IVpc Vpc { get; }
    public IPrivateHostedZone PrivateZone { get; }
    public IBucket ArtifactBucket { get; }

    public FoundationStack(Construct scope, string id, FoundationStackProps props) : base(scope, id, props)
    {
        Vpc = new Vpc(this, "Vpc", new VpcProps
        {
            VpcName = ResourceName("vpc"),
            IpAddresses = IpAddresses.Cidr(props.VpcCidr),
            MaxAzs = props.MaxAzs,
            NatGateways = props.NatGateways,
            SubnetConfiguration =
            [
                new SubnetConfiguration
                {
                    Name = "Public",
                    SubnetType = SubnetType.PUBLIC,
                    CidrMask = 24,
                },
                new SubnetConfiguration
                {
                    Name = "Private",
                    SubnetType = SubnetType.PRIVATE_WITH_EGRESS,
                    CidrMask = 24,
                },
            ],
        });

        PrivateZone = new PrivateHostedZone(this, "PrivateZone", new PrivateHostedZoneProps
        {
            ZoneName = props.InternalDomain,
            Vpc = Vpc,
        });

        ArtifactBucket = new Bucket(this, "ArtifactBucket", new BucketProps
        {
            BucketName = ResourceName("artifacts"),
            Encryption = BucketEncryption.S3_MANAGED,
            BlockPublicAccess = BlockPublicAccess.BLOCK_ALL,
            RemovalPolicy = RemovalPolicy.RETAIN,
            Versioned = true,
        });

        // Outputs for dependent stacks
        new CfnOutput(this, "VpcId", new CfnOutputProps { Value = Vpc.VpcId });
        new CfnOutput(this, "PrivateZoneId", new CfnOutputProps { Value = PrivateZone.HostedZoneId });
        new CfnOutput(this, "ArtifactBucketName", new CfnOutputProps { Value = ArtifactBucket.BucketName });
    }
}
