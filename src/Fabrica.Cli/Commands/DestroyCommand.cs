using System.CommandLine;
using System.Diagnostics;
using Spectre.Console;

namespace Fabrica.Cli.Commands;

public class DestroyCommand : Command
{
    private static readonly Option<bool> AllOption = new("--all") { Description = "Destroy all modules" };

    public DestroyCommand() : base("destroy", "Tear down Fabrica infrastructure")
    {
        Add(AllOption);
        SetAction(async (result, _) =>
        {
            var all = result.GetValue(AllOption);
            await Execute(all);
        });
    }

    private static async Task Execute(bool all)
    {
        if (!all)
        {
            AnsiConsole.MarkupLine("[yellow]Specify --all to destroy all modules, or use module-specific commands.[/]");
            return;
        }

        AnsiConsole.MarkupLine("[bold red]This will destroy ALL Fabrica infrastructure.[/]");
        AnsiConsole.MarkupLine("Stacks will be destroyed in reverse dependency order: build-farm -> perforce -> foundation");
        AnsiConsole.WriteLine();

        if (!AnsiConsole.Confirm("Are you sure?", false))
        {
            AnsiConsole.MarkupLine("[dim]Cancelled.[/]");
            return;
        }

        var config = LoadConfigOrDefault();

        // Destroy in reverse dependency order
        var stacks = new[] { "build-farm", "perforce", "foundation" };
        foreach (var module in stacks)
        {
            var stackName = $"{config.Project}-{module}";
            AnsiConsole.MarkupLine($"Destroying [bold]{stackName}[/]...");

            var exitCode = await RunCdk($"destroy {stackName} --force", config);
            if (exitCode != 0)
            {
                AnsiConsole.MarkupLine($"[red]Failed to destroy {stackName}. Remaining stacks not destroyed.[/]");
                return;
            }
        }

        AnsiConsole.MarkupLine("[green bold]All stacks destroyed.[/]");
    }

    private static Config.FabricaConfig LoadConfigOrDefault()
    {
        var path = Config.FabricaConfig.DefaultPath;
        return File.Exists(path) ? Config.FabricaConfig.Load(path) : new Config.FabricaConfig();
    }

    private static async Task<int> RunCdk(string args, Config.FabricaConfig config)
    {
        var contextArgs = $"-c fabrica:project={config.Project} -c fabrica:region={config.Region} -c fabrica:account={config.Account}";
        var psi = new ProcessStartInfo("cdk", $"{args} {contextArgs}")
        {
            UseShellExecute = false,
            WorkingDirectory = Directory.GetCurrentDirectory(),
        };
        var process = Process.Start(psi);
        if (process == null) return 1;
        await process.WaitForExitAsync();
        return process.ExitCode;
    }
}
