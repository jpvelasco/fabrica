// Package trigger implements "fabrica ci trigger <buildgraph>": start a CI build
// run that submits a BuildGraph job to the Horde coordinator.
package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/horde/buildgraph"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName   = "ci"
	hordeModule  = "horde"
	waitInterval = 15 * time.Second
	waitDeadline = 60 * time.Minute
	defaultPort  = 5000
	instanceType = "AWS::EC2::Instance"
	projectType  = "AWS::CodeBuild::Project"
)

type command struct {
	runtime        globals.Runtime
	buildGraphPath string
	wait           bool
	out            io.Writer

	readState   func() (*fabricastate.State, error)
	getResource func(ctx context.Context, r *cloud.Resource) error
	runner      cloud.CodeBuildRunner
	sleep       func(time.Duration)
	now         func() time.Time
}

// New returns the "ci trigger" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "trigger <buildgraph-file>",
		Short: "Trigger a build run (submits a BuildGraph job to Horde)",
		Long: `Start a CI build run for the given BuildGraph file.

Parses the BuildGraph XML to extract the job name and target, resolves the Horde
coordinator's address from state, and starts the CodeBuild project with those
values as environment overrides. The build's buildspec submits the job to Horde.

Requires 'fabrica ci setup' and a provisioned, reachable Horde coordinator.

Use --wait / -w to poll until the build reaches a terminal state (60m timeout).`,
		Example: `  # Fire-and-forget — prints a build ID you can track later:
  fabrica ci trigger BuildGraph.xml

  # Block until the build finishes (or 60m timeout), exit non-zero on failure:
  fabrica ci trigger BuildGraph.xml --wait

  # Prereqs: 'fabrica ci setup' has run and Horde is reachable
  # ('fabrica horde status' shows ready).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			c := command{
				runtime:        rt,
				buildGraphPath: args[0],
				wait:           wait,
				out:            out,
				readState:      func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				sleep:          time.Sleep,
				now:            time.Now,
			}
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					c.getResource = rc.Get
				}
				if r, ok := rt.Provider.(cloud.CodeBuildRunner); ok {
					c.runner = r
				}
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "Poll until the build completes or 60 minutes elapsed")
	return cmd
}

func (c command) run(ctx context.Context) error {
	// Parse BuildGraph first — fail fast before any AWS calls.
	job, err := buildgraph.ParseBuildGraph(c.buildGraphPath)
	if err != nil {
		return err
	}

	if c.runner == nil {
		return fmt.Errorf("no CodeBuild-capable cloud provider configured — check your credentials and that cloud.provider is \"aws\"")
	}

	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	project, err := c.resolveProject(st)
	if err != nil {
		return err
	}

	hordeURL, err := c.resolveHordeURL(ctx, st)
	if err != nil {
		return err
	}

	env := map[string]string{
		"BUILDGRAPH": job.Name,
		"TARGET":     job.Target,
		"HORDE_URL":  hordeURL,
	}

	buildID, err := c.runner.StartBuild(ctx, project, env)
	if err != nil {
		return fmt.Errorf("starting build: %w", err)
	}
	fmt.Fprintf(c.out, "Build started: %s\n", buildID)
	fmt.Fprintf(c.out, "  Project: %s\n", project)
	fmt.Fprintf(c.out, "  Target:  %s\n", job.Target)

	if !c.wait {
		fmt.Fprintf(c.out, "\nTrack it with: fabrica ci status   or   fabrica ci logs %s\n", buildID)
		return nil
	}
	return c.pollUntilDone(ctx, buildID)
}

func (c command) resolveProject(st *fabricastate.State) (string, error) {
	m := st.GetModule(moduleName)
	if m == nil {
		return "", fmt.Errorf("CI is not provisioned. Run 'fabrica ci setup' first.")
	}
	proj, ok := stateutil.ResourceByType(m, projectType)
	if !ok || proj.Identifier == "" {
		return "", fmt.Errorf("CI project not found in state. Run 'fabrica ci setup' first.")
	}
	return proj.Identifier, nil
}

func (c command) resolveHordeURL(ctx context.Context, st *fabricastate.State) (string, error) {
	m := st.GetModule(hordeModule)
	if m == nil {
		return "", fmt.Errorf("Horde is not provisioned. Run 'fabrica horde create' first.")
	}
	inst, ok := stateutil.ResourceByType(m, instanceType)
	if !ok || inst.Identifier == "" {
		return "", fmt.Errorf("Horde instance not found. Run 'fabrica horde status' to check.")
	}
	if c.getResource == nil {
		return "", fmt.Errorf("cannot resolve Horde address: no cloud provider")
	}
	r := &cloud.Resource{TypeName: instanceType, Identifier: inst.Identifier}
	if err := c.getResource(ctx, r); err != nil {
		return "", fmt.Errorf("querying Horde instance %s: %w", inst.Identifier, err)
	}
	ip := privateIP(r.ActualState)
	if ip == "" {
		return "", fmt.Errorf("Horde instance has no private IP yet. Run 'fabrica horde status'.")
	}
	port := defaultPort
	if c.runtime.Config != nil && c.runtime.Config.Horde.Port > 0 {
		port = c.runtime.Config.Horde.Port
	}
	return fmt.Sprintf("http://%s:%d", ip, port), nil
}

func (c command) pollUntilDone(ctx context.Context, buildID string) error {
	deadline := c.now().Add(waitDeadline)
	for {
		info, err := c.runner.BuildStatus(ctx, buildID)
		if err != nil {
			return fmt.Errorf("polling build %s: %w", buildID, err)
		}
		fmt.Fprintf(c.out, "Build %s: %s (%s)\n", buildID, info.Status, info.Phase)

		if isTerminal(info.Status) {
			if info.Status != "SUCCEEDED" {
				return fmt.Errorf("build %s finished with status %s — inspect logs with 'fabrica ci logs %s'", buildID, info.Status, buildID)
			}
			return nil
		}
		if c.now().After(deadline) {
			fmt.Fprintf(c.out, "Timed out waiting for build %s (60 minutes). Check 'fabrica ci status'.\n", buildID)
			return nil
		}
		c.sleep(waitInterval)
	}
}

func isTerminal(status string) bool {
	switch status {
	case "SUCCEEDED", "FAILED", "FAULT", "STOPPED", "TIMED_OUT":
		return true
	}
	return false
}

func privateIP(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var actual struct {
		PrivateIPAddress string `json:"PrivateIpAddress"`
	}
	if err := json.Unmarshal(raw, &actual); err != nil {
		return ""
	}
	return actual.PrivateIPAddress
}
