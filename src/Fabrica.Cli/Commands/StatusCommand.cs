using System.CommandLine;
using Spectre.Console;
using Amazon.CloudFormation;
using Amazon.CloudFormation.Model;

namespace Fabrica.Cli.Commands;

public class StatusCommand : Command
{
    public StatusCommand() : base("status", "Show health and status of all Fabrica modules")
    {
        SetAction(async (_, _) => await Execute());
    }

    private static async Task Execute()
    {
        AnsiConsole.MarkupLine("[bold]fabrica status[/]");
        AnsiConsole.WriteLine();

        var cfn = new AmazonCloudFormationClient();

        var modules = new[] { "foundation", "perforce", "build-farm" };

        var table = new Table();
        table.AddColumn("Module");
        table.AddColumn("Stack");
        table.AddColumn("Status");
        table.AddColumn("Updated");

        foreach (var module in modules)
        {
            try
            {
                var response = await cfn.DescribeStacksAsync(new DescribeStacksRequest());
                var stack = response.Stacks.FirstOrDefault(s =>
                    s.StackName.EndsWith($"-{module}") && s.StackStatus != StackStatus.DELETE_COMPLETE);

                if (stack != null)
                {
                    var statusColor = stack.StackStatus.Value.Contains("COMPLETE") ? "green" :
                                      stack.StackStatus.Value.Contains("PROGRESS") ? "yellow" : "red";

                    var timestamp = stack.LastUpdatedTime != default ? stack.LastUpdatedTime : stack.CreationTime;

                    table.AddRow(
                        module,
                        stack.StackName,
                        $"[{statusColor}]{stack.StackStatus}[/]",
                        $"{timestamp:yyyy-MM-dd HH:mm}"
                    );
                }
                else
                {
                    table.AddRow(module, "-", "[dim]not deployed[/]", "-");
                }
            }
            catch (Exception)
            {
                table.AddRow(module, "-", "[red]error[/]", "-");
            }
        }

        AnsiConsole.Write(table);
    }
}
