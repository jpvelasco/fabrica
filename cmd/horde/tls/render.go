package tls

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/spf13/cobra"
)

const checklist = `Install these files using your existing SSM workflow after backing up MongoDB.
Preserve the installed image, MongoDB volume, .env passwords, Admin account,
ACLs, and service token. Remove Horde ports and conflicting URL/certificate
environment from the base Compose file. Install the validation script as
/usr/local/sbin/fabrica-horde-validate and the unit as horde.service.
Write /etc/horde/fabrica-runtime-contract containing 1 only after validating
the upgraded base. Deliver PEM files securely; enable private DNS and trust.
Keep HORDE_BIND_ADDRESS=127.0.0.1 until TLS, existing login, service token,
and HTTP/2 checks pass. Never bootstrap a new database/account in migration.
After verification, create /etc/horde/bootstrap-complete and publish the port.
Remove old gRPC ingress, drain and replace old agents/CI after coordinator
adoption. Run fabrica horde tls adopt. See docs/horde-tls.md for the runbook.
`

func newRender(options globals.OptionsSource, out io.Writer) *cobra.Command {
	var directory string
	cmd := &cobra.Command{
		Use: "render", Short: "Generate local Horde TLS migration files",
		// Override root provider initialization: this command is strictly local.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			explicit, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(config.Path(explicit, options().Profile))
			if err != nil {
				return err
			}
			c, err := horde.ConnectionFromConfig(cfg.Horde)
			if err != nil {
				return err
			}
			if !c.TLS {
				return fmt.Errorf("horde tls render requires horde.tls.enabled: true")
			}
			overlay, err := horde.RuntimeOverlay(c)
			if err != nil {
				return err
			}
			validation, err := horde.RuntimeValidation(c)
			if err != nil {
				return err
			}
			files := map[string]string{"fabrica-runtime.json": overlay, "fabrica-horde-validate": validation, "horde.service": horde.RuntimeUnit, "install.txt": checklist}
			if options().DryRun {
				fmt.Fprintf(out, "Would render 4 TLS migration files to %s\n", directory)
				return nil
			}
			if err := writeArtifacts(directory, files); err != nil {
				return err
			}
			fmt.Fprintf(out, "Rendered TLS migration files to %s; inspect install.txt before installation.\n", directory)
			return nil
		},
	}
	cmd.Flags().StringVar(&directory, "output-dir", "horde-tls", "Directory for inspectable migration artifacts (existing files are refused)")
	return cmd
}

func writeArtifacts(directory string, files map[string]string) error {
	for name := range files {
		if _, err := os.Lstat(filepath.Join(directory, name)); err == nil {
			return fmt.Errorf("refusing to overwrite %s", filepath.Join(directory, name))
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("creating migration output directory: %w", err)
	}
	for name, content := range files {
		path := filepath.Join(directory, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("creating migration artifact: %w", err)
		}
		_, writeErr := io.WriteString(file, content)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
