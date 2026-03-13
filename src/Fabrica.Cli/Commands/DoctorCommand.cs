using System.CommandLine;
using System.Diagnostics;
using Spectre.Console;

namespace Fabrica.Cli.Commands;

public class DoctorCommand : Command
{
    public DoctorCommand() : base("doctor", "Check prerequisites and environment health")
    {
        SetAction(async (_, _) => await Execute());
    }

    private static async Task Execute()
    {
        AnsiConsole.MarkupLine("[bold]fabrica doctor[/]");
        AnsiConsole.WriteLine();

        var checks = new (string Name, Func<Task<(bool Ok, string Detail)>> Check)[]
        {
            ("AWS CLI", CheckAwsCli),
            ("AWS credentials", CheckAwsCredentials),
            ("Node.js", CheckNodeJs),
            ("CDK CLI", CheckCdkCli),
            (".NET SDK", CheckDotNet),
            ("fabrica.yaml", CheckConfig),
        };

        var passed = 0;
        var failed = 0;

        foreach (var (name, check) in checks)
        {
            var (ok, detail) = await check();
            if (ok)
            {
                AnsiConsole.MarkupLine($"  [green]PASS[/]  {name}: {detail}");
                passed++;
            }
            else
            {
                AnsiConsole.MarkupLine($"  [red]FAIL[/]  {name}: {detail}");
                failed++;
            }
        }

        AnsiConsole.WriteLine();
        if (failed == 0)
            AnsiConsole.MarkupLine($"[green bold]All {passed} checks passed.[/]");
        else
            AnsiConsole.MarkupLine($"[yellow]{passed} passed, {failed} failed.[/]");
    }

    private static async Task<(bool, string)> CheckAwsCli()
    {
        var version = await RunCommand("aws", "--version");
        return version != null
            ? (true, version.Split('\n')[0].Trim())
            : (false, "aws CLI not found. Install: https://aws.amazon.com/cli/");
    }

    private static async Task<(bool, string)> CheckAwsCredentials()
    {
        var result = await RunCommand("aws", "sts get-caller-identity --output text --query Account");
        return result != null
            ? (true, $"account {result.Trim()}")
            : (false, "No valid AWS credentials. Run: aws configure");
    }

    private static async Task<(bool, string)> CheckNodeJs()
    {
        var version = await RunCommand("node", "--version");
        return version != null
            ? (true, version.Trim())
            : (false, "Node.js not found. Required for CDK CLI. Install: https://nodejs.org/");
    }

    private static async Task<(bool, string)> CheckCdkCli()
    {
        var version = await RunCommand("cdk", "--version");
        return version != null
            ? (true, version.Trim())
            : (false, "CDK CLI not found. Install: npm install -g aws-cdk");
    }

    private static async Task<(bool, string)> CheckDotNet()
    {
        var version = await RunCommand("dotnet", "--version");
        return version != null
            ? (true, version.Trim())
            : (false, ".NET SDK not found. Install: https://dotnet.microsoft.com/");
    }

    private static Task<(bool, string)> CheckConfig()
    {
        var path = Config.FabricaConfig.DefaultPath;
        return Task.FromResult(File.Exists(path)
            ? (true, path)
            : (false, "fabrica.yaml not found in current directory"));
    }

    private static async Task<string?> RunCommand(string command, string args)
    {
        try
        {
            var psi = new ProcessStartInfo(command, args)
            {
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                UseShellExecute = false,
                CreateNoWindow = true,
            };
            var process = Process.Start(psi);
            if (process == null) return null;
            var output = await process.StandardOutput.ReadToEndAsync();
            await process.WaitForExitAsync();
            return process.ExitCode == 0 ? output : null;
        }
        catch
        {
            return null;
        }
    }
}
