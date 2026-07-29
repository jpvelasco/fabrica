package remoteexec_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/perforce/internal/remoteexec"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

func TestRunScript(t *testing.T) {
	t.Run("forwards the script", func(t *testing.T) {
		var out bytes.Buffer
		var gotInstanceID string
		var gotCommands []string
		run := func(_ context.Context, instanceID string, commands []string) (cloud.RemoteResult, error) {
			gotInstanceID = instanceID
			gotCommands = commands
			return cloud.RemoteResult{}, nil
		}

		if err := remoteexec.RunScript(context.Background(), &out, run, "i-123", "backup", "echo backup"); err != nil {
			t.Fatalf("RunScript() error = %v", err)
		}
		if gotInstanceID != "i-123" {
			t.Errorf("instance ID = %q, want i-123", gotInstanceID)
		}
		if len(gotCommands) != 1 || gotCommands[0] != "echo backup" {
			t.Errorf("commands = %q, want [echo backup]", gotCommands)
		}
		if got := out.String(); got != "Running backup via SSM...\n" {
			t.Errorf("output = %q", got)
		}
	})

	t.Run("explains remote failures", func(t *testing.T) {
		remoteErr := errors.New("SSM unavailable")
		run := func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{Stderr: "agent offline"}, remoteErr
		}

		err := remoteexec.RunScript(context.Background(), &bytes.Buffer{}, run, "i-123", "restore", "echo restore")
		if !errors.Is(err, remoteErr) {
			t.Fatalf("RunScript() error = %v, want wrapped remote error", err)
		}
		for _, want := range []string{"restore remote command failed", "AmazonSSMManagedInstanceCore", "stderr: agent offline"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want %q", err, want)
			}
		}
	})

	t.Run("reports a nonzero exit", func(t *testing.T) {
		run := func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{ExitCode: 5, Stderr: "restore failed"}, nil
		}

		err := remoteexec.RunScript(context.Background(), &bytes.Buffer{}, run, "i-123", "restore", "echo restore")
		if err == nil || !strings.Contains(err.Error(), "restore script exit 5: restore failed") {
			t.Fatalf("RunScript() error = %v", err)
		}
	})
}
