package tls

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/hordeapi"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/prompt"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

type adoptCommand struct {
	runtime   globals.Runtime
	options   globals.Options
	out       io.Writer
	read      func(context.Context) (*state.State, error)
	write     func(context.Context, *state.State) error
	get       func(context.Context, *cloud.Resource) error
	runRemote func(context.Context, string, []string) (cloud.RemoteResult, error)
	verify    func(context.Context, horde.Connection, string) error
	confirm   func(string) bool
}

func newAdopt(runtime globals.RuntimeSource, options globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{Use: "adopt", Short: "Verify TLS on the recorded coordinator and adopt its connection",
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := runtime()
			if err != nil {
				return err
			}
			c := adoptCommand{runtime: rt, options: options(), out: out, confirm: prompt.Confirm}
			c.read = func(ctx context.Context) (*state.State, error) { return provision.ReadState(ctx, rt) }
			c.write = func(ctx context.Context, st *state.State) error { return provision.StateWriter(ctx, rt)(st) }
			if rt.Provider != nil {
				c.get = rt.Provider.Resources().Get
				if remote, ok := rt.Provider.(cloud.RemoteRunner); ok {
					c.runRemote = remote.RunCommand
				}
			}
			c.verify = func(ctx context.Context, connection horde.Connection, ip string) error {
				client, err := hordeapi.NewClient(connection, rt.ConfigPath, rt.Config.Horde.ClientCAPath, ip, 5*time.Second)
				if err != nil {
					return err
				}
				return hordeapi.Probe(ctx, client, connection, ip)
			}
			return c.run(cmd.Context())
		},
	}
}

func (c adoptCommand) run(ctx context.Context) error {
	desired, err := horde.ConnectionFromConfig(c.runtime.Config.Horde)
	if err != nil {
		return err
	}
	if !desired.TLS {
		return fmt.Errorf("TLS adoption requires horde.tls.enabled: true")
	}
	ctx, release, err := provision.AcquireStateLockUnlessDryRun(ctx, c.runtime, "horde tls adopt", c.options.DryRun)
	if err != nil {
		return err
	}
	defer release()
	st, err := c.read(ctx)
	if err != nil {
		return err
	}
	m := st.GetModule("horde")
	if m == nil {
		return fmt.Errorf("Horde coordinator is not recorded; run 'fabrica horde create' first")
	}
	recorded, err := horde.RecordedConnection(m)
	if err != nil {
		return err
	}
	if recorded != nil && recorded.TLS && *recorded != desired {
		return fmt.Errorf("TLS adoption cannot change an existing TLS identity; restore the recorded config")
	}
	inst, hasInst := horde.CoordinatorResource(m, cloud.TypeAWSEC2Instance)
	sg, hasSG := horde.CoordinatorResource(m, cloud.TypeAWSEC2SecurityGroup)
	if !hasInst || !hasSG || inst.Identifier == "" || sg.Identifier == "" {
		return fmt.Errorf("TLS adoption requires the recorded coordinator instance and security group")
	}
	fmt.Fprintf(c.out, "Verify the installed coordinator at %s over SSM and pinned private-IP HTTPS; record its connection on the coordinator instance and SG.\n", desired.URL(""))
	if c.options.DryRun {
		fmt.Fprintln(c.out, "Dry run: no remote checks or state changes.")
		return nil
	}
	if !provision.ConfirmSetup(c.out, "Adopt this verified TLS coordinator in Fabrica state?", c.options.AssumeYes, c.confirm) {
		return nil
	}
	if c.runRemote == nil || c.get == nil {
		return fmt.Errorf("TLS adoption requires SSM and resource inspection support")
	}
	resource := &cloud.Resource{TypeName: inst.TypeName, Identifier: inst.Identifier}
	if err := c.get(ctx, resource); err != nil {
		return fmt.Errorf("querying recorded Horde instance: %w", err)
	}
	var actual struct {
		PrivateIP string `json:"PrivateIpAddress"`
	}
	if err := json.Unmarshal(resource.ActualState, &actual); err != nil || actual.PrivateIP == "" {
		return fmt.Errorf("recorded Horde instance has no valid private IP")
	}
	script, err := horde.AdoptionVerifier(desired)
	if err != nil {
		return err
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	result, err := c.runRemote(verifyCtx, inst.Identifier, []string{script})
	if err != nil || result.ExitCode != 0 {
		if verifyCtx.Err() != nil {
			return fmt.Errorf("Horde host verification interrupted: %w", verifyCtx.Err())
		}
		return fmt.Errorf("Horde host verification failed; check the runtime contract, effective listeners, and certificate validation over SSM")
	}
	var host struct {
		Verified bool `json:"verified"`
		Contract int  `json:"contract"`
		Port     int  `json:"port"`
	}
	if len(result.Stdout) > 4096 || json.Unmarshal([]byte(result.Stdout), &host) != nil || !host.Verified || host.Contract != 1 || host.Port != desired.Port {
		return fmt.Errorf("Horde host verifier did not return the supported TLS contract")
	}
	if err := c.verify(verifyCtx, desired, actual.PrivateIP); err != nil {
		return fmt.Errorf("verifying recorded coordinator HTTPS: %w", err)
	}
	for i := range m.Resources {
		r := &m.Resources[i]
		if !horde.IsCoordinator(*r) {
			continue
		}
		if r.Properties == nil {
			r.Properties = make(map[string]string)
		}
		r.Properties[horde.ConnectionProperty] = desired.Record()
	}
	if err := c.write(ctx, st); err != nil {
		return fmt.Errorf("persisting Horde TLS adoption: %w", err)
	}
	fmt.Fprintln(c.out, "Coordinator TLS connection adopted. Existing agent pools and CI projects require verified replacement.")
	return nil
}
