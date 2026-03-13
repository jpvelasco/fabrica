using System.CommandLine;
using System.Diagnostics;
using Spectre.Console;

namespace Fabrica.Cli.Commands;

public class SetupCommand : Command
{
    public SetupCommand() : base("setup", "Guided wizard for initial infrastructure provisioning")
    {
        SetAction(async (_, _) => await Execute());
    }

    private static async Task Execute()
    {
        AnsiConsole.MarkupLine("[bold]fabrica setup[/]");
        AnsiConsole.WriteLine();

        // Load or create config
        var configPath = Config.FabricaConfig.DefaultPath;
        Config.FabricaConfig config;

        if (File.Exists(configPath))
        {
            config = Config.FabricaConfig.Load(configPath);
            AnsiConsole.MarkupLine($"Using config: [bold]{configPath}[/]");
        }
        else
        {
            AnsiConsole.MarkupLine("[yellow]No fabrica.yaml found. Starting interactive setup.[/]");
            AnsiConsole.WriteLine();

            var project = AnsiConsole.Ask("Project name:", "my-studio");
            var region = AnsiConsole.Ask("AWS region:", "us-east-1");

            var modules = AnsiConsole.Prompt(
                new MultiSelectionPrompt<string>()
                    .Title("Which modules do you want to deploy?")
                    .AddChoices("Perforce Helix Core", "Build Farm")
                    .Select("Perforce Helix Core"));

            config = new Config.FabricaConfig
            {
                Project = project,
                Region = region,
                Perforce = modules.Contains("Perforce Helix Core") ? new Config.PerforceConfig() : null,
                BuildFarm = modules.Contains("Build Farm") ? new Config.BuildFarmConfig() : null,
            };

            // TODO: Write config to fabrica.yaml
            AnsiConsole.MarkupLine($"[dim]Config: project={config.Project}, region={config.Region}[/]");
        }

        // TODO: Cost estimation phase — show costs before deploying
        AnsiConsole.MarkupLine("[bold]Cost estimate:[/]");
        AnsiConsole.MarkupLine("[dim]  (cost estimation not yet implemented)[/]");
        AnsiConsole.WriteLine();

        if (!AnsiConsole.Confirm("Proceed with deployment?"))
        {
            AnsiConsole.MarkupLine("[dim]Cancelled.[/]");
            return;
        }

        // Deploy via CDK
        var stacks = new List<string> { "foundation" };
        if (config.Perforce != null) stacks.Add("perforce");
        if (config.BuildFarm != null) stacks.Add("build-farm");

        var stackNames = string.Join(" ", stacks.Select(s => $"{config.Project}-{s}"));
        AnsiConsole.MarkupLine($"Deploying stacks: [bold]{stackNames}[/]");
        AnsiConsole.WriteLine();

        var contextArgs = $"-c fabrica:project={config.Project} -c fabrica:region={config.Region} -c fabrica:account={config.Account}";
        var exitCode = await RunCdk($"deploy {stackNames} --require-approval broadening {contextArgs}", config);

        if (exitCode == 0)
            AnsiConsole.MarkupLine("[green bold]Setup complete.[/]");
        else
            AnsiConsole.MarkupLine("[red]Deployment failed. Check the output above for details.[/]");
    }

    private static async Task<int> RunCdk(string args, Config.FabricaConfig config)
    {
        var psi = new ProcessStartInfo("cdk", args)
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
