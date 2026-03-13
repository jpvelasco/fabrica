namespace Fabrica.Constructs.Shared;

public static class Tags
{
    public const string ManagedBy = "ManagedBy";
    public const string ManagedByValue = "fabrica";
    public const string Module = "fabrica:Module";
    public const string Version = "fabrica:Version";
    public const string Project = "fabrica:Project";

    public static Dictionary<string, string> Standard(string moduleName, string projectName) => new()
    {
        [ManagedBy] = ManagedByValue,
        [Module] = moduleName,
        [Project] = projectName,
        [Version] = typeof(Tags).Assembly.GetName().Version?.ToString() ?? "0.0.0",
    };
}
