package submit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/horde"
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
	readState          func() (*fabricastate.State, error)
	hordeClient        HordeClient
	newClient          func(baseURL, token string) HordeClient
	getSecureParameter func(ctx context.Context, name string) (string, error)
	waitCtx            func(context.Context, time.Duration) error
	now                func() time.Time
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
(times out after 60 minutes). With --wait the command exits non-zero when
the job finishes in error, the wait times out, or polling is cancelled (the
job keeps running on the coordinator either way). Use --dry-run to preview
the job and the coordinator address without submitting.

The request authenticates with the fabrica-ci service-account token that the
coordinator's cloud-init publishes to the SSM SecureString recorded at create
time (/fabrica/horde/<create-nonce>/service-token); reading it requires
ssm:GetParameter on that parameter. --dry-run never reads the token.

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
			if rd, ok := rt.Provider.(cloud.SecureParameterReader); ok {
				c.getSecureParameter = rd.GetSecureParameter
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
	instRes, hasInst := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	tokenParam := instRes.Properties["serviceTokenParameter"]
	if client == nil {
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
		c.printDryRun(job, baseURL, tokenParam)
		return nil
	}

	if client == nil {
		token, err := c.serviceToken(ctx, tokenParam)
		if err != nil {
			return err
		}
		newClient := c.newClient
		if newClient == nil {
			newClient = func(baseURL, token string) HordeClient { return newHordeHTTPClient(baseURL, token) }
		}
		client = newClient(baseURL, token)
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

// serviceToken reads the fabrica-ci service-account token from the recorded
// SSM SecureString. A coordinator created before service tokens records no
// parameter; submit then sends no credentials, which only an unauthenticated
// (pre-hardening) Horde accepts.
func (c command) serviceToken(ctx context.Context, name string) (string, error) {
	if name == "" {
		fmt.Fprintln(c.out, "Note: this Horde coordinator records no service-token parameter (created before service tokens); submitting without credentials. Re-create it to enable authentication.")
		return "", nil
	}
	if !horde.IsServiceTokenName(name) {
		return "", fmt.Errorf("the recorded service-token parameter %q is not a per-create %s name; re-run 'fabrica horde create'", name, horde.ServiceTokenParameterPrefix)
	}
	if c.getSecureParameter == nil {
		return "", fmt.Errorf("reading the Horde service token from SSM parameter %s requires a provider with SSM parameter support", name)
	}
	token, err := c.getSecureParameter(ctx, name)
	if err != nil {
		if errors.Is(err, cloud.ErrParameterNotFound) {
			return "", fmt.Errorf("the Horde service-token parameter %s does not exist; re-run 'fabrica horde create'", name)
		}
		return "", fmt.Errorf("reading the Horde service token: %w", err)
	}
	if strings.TrimSpace(token) == "" || token == horde.ServiceTokenPlaceholder {
		return "", fmt.Errorf("the coordinator has not published its service token to %s yet. Run 'fabrica horde status -w'; if it stays unpublished, check /var/log/fabrica-horde-init.log on the instance", name)
	}
	return token, nil
}

// printDryRun previews the job submission without contacting the coordinator.
func (c command) printDryRun(job *buildgraph.BuildGraphJob, baseURL, tokenParam string) {
	fmt.Fprintln(c.out, "Horde submit (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  job:    %s\n", job.Name)
	fmt.Fprintf(c.out, "  target: %s\n", job.Target)
	if baseURL != "" {
		fmt.Fprintf(c.out, "  coordinator: %s\n", baseURL)
	}
	if tokenParam != "" {
		fmt.Fprintf(c.out, "  auth:   %s token from SSM %s\n", horde.ServiceTokenAuthScheme, tokenParam)
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

		switch state {
		case "complete":
			return nil
		case "error":
			return fmt.Errorf("job %s finished in error — inspect it in the Horde web UI", jobID)
		}

		if c.now().After(deadline) {
			return fmt.Errorf("timed out waiting for job %s after 60 minutes — job %s is still running; monitor it in the Horde web UI", jobID, jobID)
		}

		if err := c.waitCtx(ctx, waitInterval); err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return fmt.Errorf("waiting for job %s cancelled: %w — job %s continues on the coordinator; check its state in the Horde web UI", jobID, err, jobID)
			}
			return fmt.Errorf("waiting for job %s: %w", jobID, err)
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
