using YamlDotNet.Serialization;

namespace Fabrica.Cli.Config;

public class FabricaConfig
{
    [YamlMember(Alias = "project")]
    public string Project { get; set; } = "fabrica";

    [YamlMember(Alias = "region")]
    public string Region { get; set; } = "us-east-1";

    [YamlMember(Alias = "account")]
    public string Account { get; set; } = "";

    [YamlMember(Alias = "foundation")]
    public FoundationConfig Foundation { get; set; } = new();

    [YamlMember(Alias = "perforce")]
    public PerforceConfig? Perforce { get; set; }

    [YamlMember(Alias = "build_farm")]
    public BuildFarmConfig? BuildFarm { get; set; }

    public static FabricaConfig Load(string path)
    {
        var yaml = File.ReadAllText(path);
        var deserializer = new DeserializerBuilder().Build();
        return deserializer.Deserialize<FabricaConfig>(yaml) ?? new FabricaConfig();
    }

    public static string DefaultPath => Path.Combine(Directory.GetCurrentDirectory(), "fabrica.yaml");
}

public class FoundationConfig
{
    [YamlMember(Alias = "vpc_cidr")]
    public string VpcCidr { get; set; } = "10.0.0.0/16";

    [YamlMember(Alias = "max_azs")]
    public int MaxAzs { get; set; } = 2;

    [YamlMember(Alias = "nat_gateways")]
    public int NatGateways { get; set; } = 1;

    [YamlMember(Alias = "internal_domain")]
    public string InternalDomain { get; set; } = "studio.internal";
}

public class PerforceConfig
{
    [YamlMember(Alias = "instance_type")]
    public string InstanceType { get; set; } = "c6i.large";

    [YamlMember(Alias = "depot_volume_gb")]
    public int DepotVolumeGb { get; set; } = 256;

    [YamlMember(Alias = "metadata_volume_gb")]
    public int MetadataVolumeGb { get; set; } = 64;

    [YamlMember(Alias = "logs_volume_gb")]
    public int LogsVolumeGb { get; set; } = 32;
}

public class BuildFarmConfig
{
    [YamlMember(Alias = "coordinator_instance_type")]
    public string CoordinatorInstanceType { get; set; } = "c6i.xlarge";

    [YamlMember(Alias = "worker_instance_type")]
    public string WorkerInstanceType { get; set; } = "c6i.4xlarge";

    [YamlMember(Alias = "min_workers")]
    public int MinWorkers { get; set; } = 0;

    [YamlMember(Alias = "max_workers")]
    public int MaxWorkers { get; set; } = 4;
}
