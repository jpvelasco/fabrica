using Amazon.CDK;
using Constructs;

namespace Fabrica.Constructs.Shared;

public record FabricaConstructProps
{
    public required string ProjectName { get; init; }
    public required string ModuleName { get; init; }
}

/// <summary>
/// Base construct for all Fabrica resources.
/// Enforces project-prefixed naming and standard tags on every resource.
/// </summary>
public abstract class FabricaConstruct : Construct
{
    public string ProjectName { get; }
    public string ModuleName { get; }

    protected FabricaConstruct(Construct scope, string id, FabricaConstructProps props) : base(scope, id)
    {
        ProjectName = props.ProjectName;
        ModuleName = props.ModuleName;

        var tags = Tags.Standard(props.ModuleName, props.ProjectName);
        foreach (var (key, value) in tags)
        {
            Amazon.CDK.Tags.Of(this).Add(key, value);
        }
    }

    protected string ResourceName(string name) => Naming.Resource(ProjectName, ModuleName, name);
}
