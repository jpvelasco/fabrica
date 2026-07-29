// Package remoteexec owns the shared Perforce SSM script lifecycle.
package remoteexec

import (
	"context"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

// Runner executes commands on a remote instance.
type Runner func(context.Context, string, []string) (cloud.RemoteResult, error)

// RunScript executes one named Perforce operation through SSM.
func RunScript(ctx context.Context, out io.Writer, run Runner, instanceID, operation, script string) error {
	fmt.Fprintf(out, "Running %s via SSM...\n", operation)
	result, err := run(ctx, instanceID, []string{script})
	if err != nil {
		return fmt.Errorf("%s remote command failed: %w\nIf the instance has no SSM profile, recreate Perforce with a current Fabrica or attach AmazonSSMManagedInstanceCore and retry.\nstderr: %s", operation, err, result.Stderr)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s script exit %d: %s", operation, result.ExitCode, result.Stderr)
	}
	return nil
}
