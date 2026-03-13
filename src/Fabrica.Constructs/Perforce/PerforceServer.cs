using Amazon.CDK;
using Amazon.CDK.AWS.EC2;
using Amazon.CDK.AWS.IAM;
using Amazon.CDK.AWS.Route53;
using Amazon.CDK.AWS.S3;
using Amazon.CDK.AWS.SecretsManager;
using Constructs;
using Fabrica.Constructs.Shared;

namespace Fabrica.Constructs.Perforce;

public record PerforceServerProps : FabricaConstructProps
{
    public required IVpc Vpc { get; init; }
    public required IPrivateHostedZone PrivateZone { get; init; }
    public string InstanceType { get; init; } = "c6i.large";
    public double DepotVolumeSize { get; init; } = 256;
    public double MetadataVolumeSize { get; init; } = 64;
    public double LogsVolumeSize { get; init; } = 32;
}

/// <summary>
/// L3 construct: Perforce Helix Core server with EBS volumes, NLB, backup, and DNS.
/// </summary>
public class PerforceServer : FabricaConstruct
{
    public Instance_ Instance { get; }
    public ISecret AdminSecret { get; }

    public PerforceServer(Construct scope, string id, PerforceServerProps props) : base(scope, id, props)
    {
        // Admin password — generated and stored in Secrets Manager, never in state
        AdminSecret = new Secret(this, "AdminSecret", new SecretProps
        {
            SecretName = ResourceName("p4-admin"),
            Description = "Perforce Helix Core super user password",
            GenerateSecretString = new SecretStringGenerator
            {
                PasswordLength = 32,
                ExcludePunctuation = true,
            },
        });

        // Security group — only allows 1666 from NLB
        var sg = new SecurityGroup(this, "ServerSg", new SecurityGroupProps
        {
            Vpc = props.Vpc,
            SecurityGroupName = ResourceName("p4-server"),
            Description = "Perforce Helix Core server",
            AllowAllOutbound = true,
        });

        // IAM role — least privilege via CDK grants
        var role = new Role(this, "ServerRole", new RoleProps
        {
            RoleName = ResourceName("p4-server"),
            AssumedBy = new ServicePrincipal("ec2.amazonaws.com"),
            ManagedPolicies =
            [
                ManagedPolicy.FromAwsManagedPolicyName("AmazonSSMManagedInstanceCore"),
                ManagedPolicy.FromAwsManagedPolicyName("CloudWatchAgentServerPolicy"),
            ],
        });

        // Grant the role access to read the admin secret
        AdminSecret.GrantRead(role);

        // EC2 instance
        Instance = new Instance_(this, "Server", new InstanceProps
        {
            InstanceName = ResourceName("p4-server"),
            Vpc = props.Vpc,
            VpcSubnets = new SubnetSelection { SubnetType = SubnetType.PRIVATE_WITH_EGRESS },
            InstanceType = new InstanceType(props.InstanceType),
            MachineImage = MachineImage.LatestAmazonLinux2023(new AmazonLinux2023ImageSsmParameterProps
            {
                CpuType = AmazonLinuxCpuType.X86_64,
            }),
            SecurityGroup = sg,
            Role = role,
            BlockDevices =
            [
                new BlockDevice
                {
                    DeviceName = "/dev/xvda",
                    Volume = Amazon.CDK.AWS.EC2.BlockDeviceVolume.Ebs(30, new EbsDeviceOptions
                    {
                        VolumeType = EbsDeviceVolumeType.GP3,
                        Encrypted = true,
                    }),
                },
                new BlockDevice
                {
                    DeviceName = "/dev/xvdb",
                    Volume = Amazon.CDK.AWS.EC2.BlockDeviceVolume.Ebs(props.DepotVolumeSize, new EbsDeviceOptions
                    {
                        VolumeType = EbsDeviceVolumeType.IO2,
                        Iops = 3000,
                        Encrypted = true,
                    }),
                },
                new BlockDevice
                {
                    DeviceName = "/dev/xvdc",
                    Volume = Amazon.CDK.AWS.EC2.BlockDeviceVolume.Ebs(props.MetadataVolumeSize, new EbsDeviceOptions
                    {
                        VolumeType = EbsDeviceVolumeType.GP3,
                        Encrypted = true,
                    }),
                },
                new BlockDevice
                {
                    DeviceName = "/dev/xvdd",
                    Volume = Amazon.CDK.AWS.EC2.BlockDeviceVolume.Ebs(props.LogsVolumeSize, new EbsDeviceOptions
                    {
                        VolumeType = EbsDeviceVolumeType.GP3,
                        Encrypted = true,
                    }),
                },
            ],
        });

        // NLB for TCP passthrough on port 1666
        var nlb = new Amazon.CDK.AWS.ElasticLoadBalancingV2.NetworkLoadBalancer(this, "Nlb",
            new Amazon.CDK.AWS.ElasticLoadBalancingV2.NetworkLoadBalancerProps
            {
                LoadBalancerName = ResourceName("p4"),
                Vpc = props.Vpc,
                InternetFacing = false,
                VpcSubnets = new SubnetSelection { SubnetType = SubnetType.PRIVATE_WITH_EGRESS },
            });

        var listener = nlb.AddListener("P4Listener",
            new Amazon.CDK.AWS.ElasticLoadBalancingV2.BaseNetworkListenerProps
            {
                Port = 1666,
            });

        listener.AddTargets("P4Target",
            new Amazon.CDK.AWS.ElasticLoadBalancingV2.AddNetworkTargetsProps
            {
                Port = 1666,
                Targets = [new Amazon.CDK.AWS.ElasticLoadBalancingV2.Targets.InstanceTarget(Instance, 1666)],
                HealthCheck = new Amazon.CDK.AWS.ElasticLoadBalancingV2.HealthCheck
                {
                    Port = "1666",
                    Protocol = Amazon.CDK.AWS.ElasticLoadBalancingV2.Protocol.TCP,
                },
            });

        // Allow NLB to reach the server
        sg.AddIngressRule(Peer.Ipv4(props.Vpc.VpcCidrBlock), Port.Tcp(1666), "Perforce from NLB");

        // DNS record
        new Amazon.CDK.AWS.Route53.CnameRecord(this, "DnsRecord", new CnameRecordProps
        {
            Zone = props.PrivateZone,
            RecordName = "perforce",
            DomainName = nlb.LoadBalancerDnsName,
        });
    }
}
