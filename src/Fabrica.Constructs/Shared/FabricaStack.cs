using Amazon.CDK;
using Constructs;

namespace Fabrica.Constructs.Shared;

public class FabricaStackProps : StackProps
{
    public required string ProjectName { get; set; }
    public required string ModuleName { get; set; }
}

/// <summary>
/// Base stack for all Fabrica modules.
/// Enforces standard tags at the stack level.
/// </summary>
public abstract class FabricaStack : Stack
{
    public string ProjectName { get; }
    public string ModuleName { get; }

    protected FabricaStack(Construct scope, string id, FabricaStackProps props) : base(scope, id, props)
    {
        ProjectName = props.ProjectName;
        ModuleName = props.ModuleName;

        var standardTags = Shared.Tags.Standard(props.ModuleName, props.ProjectName);
        foreach (var kvp in standardTags)
        {
            Amazon.CDK.Tags.Of(this).Add(kvp.Key, kvp.Value);
        }
    }

    protected string ResourceName(string name) => Naming.Resource(ProjectName, ModuleName, name);
}
