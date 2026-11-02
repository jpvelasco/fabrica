package submit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
	moduleName   = "horde"
	lineWidth    = 58 // match deploy promote's dry-run header width
	waitInterval = 30 * time.Second
	waitTimeout  = 60 * time.Minute

	defaultPort = 5000
)

type command struct {
	runtime        globals.Runtime
	buildGraphPath string
	dryRun         bool
	wait           bool
	out            io.Writer

	// seams for testing
	readState   func() (*fabricastate.State, error)
	hordeClient HordeClient
	waitCtx     func(context.Context, time.Duration) error
	now         func() time.Time
}

// New returns the "horde submit" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "submit <buildgraph-file>",
		Short: "Submit a BuildGraph job to the Horde coordinator",
		Long: `Submit a BuildGraph XML file as a job to the Horde build coordinator.

Parses the BuildGraph file to extract the job name and target, then POSTs to
the Horde REST API. By default, submits and returns immediately (fire-and-forget).

Use --wait / -w to poll every 30 seconds until the job completes or errors
(times out after 60 minutes). Use --dry-run to preview the job and the
coordinator address without submitting.

The Horde coordinator must be reachable from this machine (VPN or same VPC).
Run 'fabrica horde status' to confirm the coordinator is ready.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()

			c := command{
				runtime:        rt,
				buildGraphPath: args[0],
				dryRun:         opts.DryRun,
				wait:           wait,
				out:            out,
				readState:      func() (*fabricastate.State, error) { return provision.ReadState(cmd.Context(), rt) },
				waitCtx:        provision.WaitInterval,
				now:            time.Now,
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().BoolVarP(&wait, "wait", "w", false, "Poll until job completes or 60 minutes elapsed")
	return cmd
}

func (c command) run(ctx context.Context) error {
	// Parse the BuildGraph file first — fail fast before any network calls.
	job, err := buildgraph.ParseBuildGraph(c.buildGraphPath)
	if err != nil {
		return err
	}
	if err := buildgraph.ValidateJob(job); err != nil {
		return err
	}

	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(moduleName)
	if m == nil {
		return fmt.Errorf("Horde is not provisioned. Run 'fabrica horde create' first.")
	}

	// Resolve the coordinator address if no client is injected (production
	// path); the HTTP client is built after the dry-run return so a preview
	// never opens a connection.
	client := c.hordeClient
	baseURL := ""
	if client == nil {
		instRes, hasInst := stateutil.ResourceByType(m, "AWS::EC2::Instance")
		if !hasInst || instRes.Identifier == "" {
			return fmt.Errorf("Horde instance has no private IP yet. Run 'fabrica horde status' to check readiness.")
		}

		privateIP, err := c.resolvePrivateIP(ctx, instRes.Identifier)
		if err != nil {
			return err
		}
		if privateIP == "" {
			return fmt.Errorf("Horde instance has no private IP yet. Run 'fabrica horde status' to check readiness.")
		}

		port := c.runtime.Config.Horde.Port
		if port <= 0 {
			port = defaultPort
		}
		baseURL = fmt.Sprintf("http://%s:%d", privateIP, port)
	}

	if c.dryRun {
		c.printDryRun(job, baseURL)
		return nil
	}

	if client == nil {
		client = newHordeHTTPClient(baseURL, "")
	}

	jobID, err := client.SubmitJob(ctx, job)
	if err != nil {
		return fmt.Errorf("submitting job: %w", err)
	}
	fmt.Fprintf(c.out, "Job submitted: %s\n", jobID)

	if !c.wait {
		return nil
	}

	return c.pollUntilDone(ctx, client, jobID)
}

// printDryRun previews the job submission without contacting the coordinator.
func (c command) printDryRun(job *buildgraph.BuildGraphJob, baseURL string) {
	fmt.Fprintln(c.out, "Horde submit (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  job:    %s\n", job.Name)
	fmt.Fprintf(c.out, "  target: %s\n", job.Target)
	if baseURL != "" {
		fmt.Fprintf(c.out, "  coordinator: %s\n", baseURL)
	}
	fmt.Fprintln(c.out, "Run without --dry-run to submit the job.")
}

func (c command) pollUntilDone(ctx context.Context, client HordeClient, jobID string) error {
	deadline := c.now().Add(waitTimeout)
	for {
		state, err := client.GetJobStatus(ctx, jobID)
		if err != nil {
			return fmt.Errorf("polling job %s: %w", jobID, err)
		}

		fmt.Fprintf(c.out, "Job %s: %s\n", jobID, state)

		if state == "complete" || state == "error" {
			return nil
		}

		if c.now().After(deadline) {
			fmt.Fprintf(c.out, "timed out waiting for job %s to complete (60 minutes)\n", jobID)
			fmt.Fprintf(c.out, "Job ID: %s — monitor in Horde web UI\n", jobID)
			return nil
		}

		if err := c.waitCtx(ctx, waitInterval); err != nil {
			fmt.Fprintf(c.out, "waiting cancelled: %v\n", err)
			return nil
		}
	}
}

func (c command) resolvePrivateIP(ctx context.Context, instanceID string) (string, error) {
	if c.runtime.Provider == nil {
		return "", nil
	}
	r := &cloud.Resource{
		TypeName:   "AWS::EC2::Instance",
		Identifier: instanceID,
	}
	if err := c.runtime.Provider.Resources().Get(ctx, r); err != nil {
		return "", fmt.Errorf("querying instance %s: %w", instanceID, err)
	}
	if len(r.ActualState) == 0 {
		return "", nil
	}
	var actual struct {
		PrivateIPAddress string `json:"PrivateIpAddress"`
	}
	if err := json.Unmarshal(r.ActualState, &actual); err != nil {
		return "", nil
	}
	return actual.PrivateIPAddress, nil
}
