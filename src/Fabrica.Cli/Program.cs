using System.CommandLine;
using Fabrica.Cli.Commands;

var root = new RootCommand("Fabrica — game studio infrastructure on AWS")
{
    new SetupCommand(),
    new StatusCommand(),
    new DoctorCommand(),
    new DestroyCommand(),
};

return await root.Parse(args).InvokeAsync();
