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
	"github.com/jpvelasco/fabrica/cmd/internal/hordeapi"
	"github.com/jpvelasco/fabrica/cmd/internal/hordejob"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/horde/buildgraph"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	moduleName   = "horde"
	lineWidth    = 58 // match deploy promote's dry-run header width
	waitInterval = 30 * time.Second
	waitTimeout  = 60 * time.Minute
)

type command struct {
	runtime        globals.Runtime
	buildGraphPath string // optional; parsed locally for the target, never uploaded
	overrides      horde.JobOverrides
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
	var overrides horde.JobOverrides
	cmd := &cobra.Command{
		Use:   "submit [buildgraph-file]",
		Short: "Submit a job to the Horde coordinator",
		Long: `Submit a job to the Horde build coordinator.

The job runs from horde.streamId and horde.templateId (or --stream and
--template), which must exist in the coordinator's globals.json; see
docs/horde-jobs.md. A BuildGraph file is parsed locally to pick the target
(--target overrides it) and is never uploaded: Horde runs the script from the
synced stream. Without a file, the template's default target runs unless
--target is given. By default, submits and returns immediately (fire-and-forget).

Use --wait / -w to poll every 30 seconds until the job completes (times out
after 60 minutes). With --wait the command exits non-zero when any step fails or
any batch errors, when the job state is not recognised, when the wait times out,
or when polling is cancelled (the job keeps running on the coordinator in the
last two cases). Use --dry-run to preview the request and the coordinator
address without submitting.

The request authenticates with the fabrica-ci service-account token that the
coordinator's cloud-init publishes to the SSM SecureString recorded at create
time (/fabrica/horde/<create-nonce>/service-token); reading it requires
ssm:GetParameter on that parameter. --dry-run never reads the token.

The Horde coordinator must be reachable from this machine (VPN or same VPC).
Run 'fabrica horde status' to confirm the coordinator is ready.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()

			var graphPath string
			if len(args) == 1 {
				graphPath = args[0]
			}
			c := command{
				runtime:        rt,
				buildGraphPath: graphPath,
				overrides:      overrides,
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
	cmd.Flags().StringVar(&overrides.Target, "target", "", "BuildGraph <Aggregate> or <Node> name, or template target, to run (default: first <Aggregate> in the file, else first <Node>)")
	hordejob.BindFlags(cmd, &overrides)
	return cmd
}

// buildRequest builds the job request and checks it before any state or
// network access. A BuildGraph file is parsed only to resolve the target and
// the default job name; choice is nil when no file was given.
func (c command) buildRequest() (horde.CreateJobRequest, *buildgraph.TargetChoice, error) {
	o := c.overrides
	var choice *buildgraph.TargetChoice
	if c.buildGraphPath != "" {
		job, err := buildgraph.ParseBuildGraph(c.buildGraphPath)
		if err != nil {
			return horde.CreateJobRequest{}, nil, err
		}
		resolved := buildgraph.ResolveTarget(job, o.Target)
		if resolved.Warning != "" {
			fmt.Fprintf(c.out, "Warning: %s\n", resolved.Warning)
		}
		if err := buildgraph.ValidateJob(job); err != nil {
			return horde.CreateJobRequest{}, nil, err
		}
		o.Target = resolved.Name
		if o.Name == "" {
			o.Name = job.Name
		}
		choice = &resolved
	}
	req := horde.NewCreateJobRequest(c.runtime.Config.Horde, o)
	if err := req.Validate(); err != nil {
		return horde.CreateJobRequest{}, nil, err
	}
	return req, choice, nil
}

func (c command) run(ctx context.Context) error {
	req, choice, err := c.buildRequest()
	if err != nil {
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
	connection, err := horde.ResolveConnection(c.runtime.Config.Horde, m)
	if err != nil {
		return err
	}

	// Resolve the coordinator address if no client is injected (production
	// path); the HTTP client is built after the dry-run return so a preview
	// never opens a connection.
	client := c.hordeClient
	baseURL := ""
	instRes, hasInst := horde.CoordinatorResource(m, "AWS::EC2::Instance")
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

		baseURL = connection.URL(privateIP)
	}

	if c.dryRun {
		c.printDryRun(req, choice, baseURL, tokenParam)
		return nil
	}

	if client == nil {
		httpClient, err := hordeapi.NewClient(connection, c.runtime.ConfigPath, c.runtime.Config.Horde.ClientCAPath, "", hordeHTTPTimeout)
		if err != nil {
			return err
		}
		token, err := c.serviceToken(ctx, tokenParam)
		if err != nil {
			return err
		}
		newClient := c.newClient
		if newClient == nil {
			newClient = func(baseURL, token string) HordeClient {
				return &hordeHTTPClient{baseURL: baseURL, token: token, http: httpClient}
			}
		}
		client = newClient(baseURL, token)
	}

	jobID, err := client.SubmitJob(ctx, req)
	if err != nil {
		return fmt.Errorf("submitting job: %w", err)
	}
	fmt.Fprintf(c.out, "Job submitted: %s\n", jobID)

	if !c.wait {
		return nil
	}

	return c.pollUntilDone(ctx, client, jobID, baseURL)
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
		return "", fmt.Errorf("the recorded service-token parameter %q is not a per-create /<namePrefix>/horde/<nonce>/service-token name; re-run 'fabrica horde create'", name)
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

// printDryRun previews the job request without contacting the coordinator.
// choice is nil when no BuildGraph file was given; the file is then only
// described, never sent.
func (c command) printDryRun(req horde.CreateJobRequest, choice *buildgraph.TargetChoice, baseURL, tokenParam string) {
	fmt.Fprintln(c.out, "Horde submit (dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  stream: %s\n", req.StreamID)
	fmt.Fprintf(c.out, "  template: %s\n", req.TemplateID)
	switch {
	case choice != nil:
		fmt.Fprintf(c.out, "  target: %s (%s); the file is not uploaded\n", choice.Name, choice.Reason)
	case len(req.Targets) > 0:
		fmt.Fprintf(c.out, "  target: %s (--target)\n", req.Targets[0])
	default:
		fmt.Fprintln(c.out, "  target: template default")
	}
	if baseURL != "" {
		fmt.Fprintf(c.out, "  coordinator: %s\n", baseURL)
	}
	if tokenParam != "" {
		fmt.Fprintf(c.out, "  auth:   %s token from SSM %s\n", horde.ServiceTokenAuthScheme, tokenParam)
	}
	if body, err := json.Marshal(req); err == nil {
		fmt.Fprintf(c.out, "  request: %s\n", body)
	}
	fmt.Fprintln(c.out, "Run without --dry-run to submit the job.")
}

func (c command) pollUntilDone(ctx context.Context, client HordeClient, jobID, coordinatorURL string) error {
	deadline := c.now().Add(waitTimeout)
	for {
		status, err := client.GetJobStatus(ctx, jobID)
		if err != nil {
			return fmt.Errorf("polling job %s: %w", jobID, err)
		}

		fmt.Fprintf(c.out, "Job %s: %s\n", jobID, status.State)

		switch status.State {
		case horde.JobStateComplete:
			return c.reportOutcome(jobID, coordinatorURL, status)
		case horde.JobStateWaiting, horde.JobStateRunning:
		default:
			return fmt.Errorf("job %s reported state %q, which fabrica does not recognise (expected %s, %s, or %s); inspect the job in the Horde web UI",
				jobID, status.State, horde.JobStateWaiting, horde.JobStateRunning, horde.JobStateComplete)
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

// reportOutcome prints a completed job's warnings and fails when any step or
// batch did not succeed. Horde's Complete state only says the job stopped.
func (c command) reportOutcome(jobID, coordinatorURL string, status horde.JobStatus) error {
	outcome := horde.EvaluateCompleted(status)
	for _, w := range outcome.Warnings {
		fmt.Fprintf(c.out, "Warning: job %s: %s\n", jobID, w)
	}
	if !outcome.Failed() {
		return nil
	}
	return jobFailedError(jobID, coordinatorURL, outcome)
}

// jobFailedError names the job, up to MaxListedFailures failing items, and the
// next step. coordinatorURL is empty when the client was injected; the link
// is then omitted.
func jobFailedError(jobID, coordinatorURL string, o horde.JobOutcome) error {
	where := fmt.Sprintf("open job %s in the Horde dashboard", jobID)
	if coordinatorURL != "" {
		where = fmt.Sprintf("open the job in the Horde dashboard at %s/job/%s", coordinatorURL, jobID)
	}
	msg := fmt.Sprintf("job %s finished with %d failing item(s): %s", jobID, o.FailureCount, strings.Join(o.Failures, "; "))
	if more := o.FailureCount - len(o.Failures); more > 0 {
		msg += fmt.Sprintf("; and %d more", more)
	}
	msg += ". " + where + " to see the step logs."
	if o.AgentsMissing {
		msg += " For NoAgentsInPool or NoAgentsOnline, check that 'fabrica horde agents' lists enrolled agents and approve pending ones with 'fabrica horde agents approve'."
	}
	return errors.New(msg)
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
