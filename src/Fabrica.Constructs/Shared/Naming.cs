namespace Fabrica.Constructs.Shared;

public static class Naming
{
    public static string Resource(string projectName, string moduleName, string resourceName)
        => $"{projectName}-{moduleName}-{resourceName}";

    public static string Stack(string projectName, string moduleName)
        => $"{projectName}-{moduleName}";
}
