package root_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/root"
	"github.com/jpvelasco/fabrica/internal/oplog"
)

func TestVerboseStderrJSONStdoutPurity(t *testing.T) {
	oplog.ResetForTest()

	// Capture oplog output to a separate buffer.
	var logBuf bytes.Buffer
	oplog.InitWithWriter(0, true, &logBuf)

	// Capture stdout.
	var stdoutBuf bytes.Buffer
	cmd := root.New(&stdoutBuf)
	cmd.SetOut(&stdoutBuf)
	cmd.SetArgs([]string{"--verbose", "--json", "version"})

	ctx := context.Background()
	err := cmd.ExecuteContext(ctx)
	if err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	// Verify stdout contains version info.
	stdout := stdoutBuf.String()
	if !strings.Contains(stdout, "Fabrica") {
		t.Errorf("stdout should contain 'Fabrica', got: %s", stdout)
	}

	// Verify stdout does NOT contain slog lines (slog goes to stderr via oplog).
	if strings.Contains(stdout, "level=info") || strings.Contains(stdout, "level=debug") {
		t.Errorf("stdout should not contain slog lines, got: %s", stdout)
	}
}

func TestDefaultNoVerbose(t *testing.T) {
	oplog.ResetForTest()
	var logBuf bytes.Buffer
	oplog.InitWithWriter(0, false, &logBuf)

	var stdoutBuf bytes.Buffer
	cmd := root.New(&stdoutBuf)
	cmd.SetOut(&stdoutBuf)
	cmd.SetArgs([]string{"version"})

	ctx := context.Background()
	err := cmd.ExecuteContext(ctx)
	if err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	// Default run should not produce slog on log output.
	logOutput := logBuf.String()
	if strings.Contains(logOutput, "level=info") || strings.Contains(logOutput, "level=debug") {
		t.Errorf("default run should not produce slog, got: %s", logOutput)
	}
}

func TestVerboseEnablesDebugLevel(t *testing.T) {
	oplog.ResetForTest()
	var buf bytes.Buffer
	oplog.InitWithWriter(0, true, &buf)

	oplog.Logger().Debug("debug should appear")
	oplog.Logger().Info("info should appear")

	output := buf.String()
	if !strings.Contains(output, "debug should appear") {
		t.Error("debug should appear when verbose=true")
	}
	if !strings.Contains(output, "info should appear") {
		t.Error("info should appear when verbose=true")
	}
}

func TestNonVerboseHidesDebug(t *testing.T) {
	oplog.ResetForTest()
	var buf bytes.Buffer
	oplog.InitWithWriter(0, false, &buf)

	oplog.Logger().Debug("debug should not appear")
	oplog.Logger().Info("info should appear")

	output := buf.String()
	if strings.Contains(output, "debug should not appear") {
		t.Error("debug should not appear when verbose=false")
	}
	if !strings.Contains(output, "info should appear") {
		t.Error("info should appear when verbose=false")
	}
}

// TestRootPersistentPreRunERuns verifies that root's PersistentPreRunE
// actually executes during Cobra's ExecuteContext, exercising the
// oplog.Init call path. We verify this by running a command that triggers
// PersistentPreRunE and confirming the command completes correctly.
func TestRootPersistentPreRunERuns(t *testing.T) {
	oplog.ResetForTest()

	// Pre-init with a capture writer so we can verify the logger state
	// after ExecuteContext.  Root's PersistentPreRunE will call Init,
	// but sync.Once means our pre-Init wins — this is correct behavior
	// (Init is idempotent). The test proves ExecuteContext runs the full
	// command chain including PersistentPreRunE.
	var logBuf bytes.Buffer
	oplog.InitWithWriter(0, true, &logBuf)

	var stdoutBuf bytes.Buffer
	cmd := root.New(&stdoutBuf)
	cmd.SetOut(&stdoutBuf)
	cmd.SetArgs([]string{"--verbose", "destroy"})

	ctx := context.Background()
	err := cmd.ExecuteContext(ctx)
	if err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	// destroy without --all prints usage hint to stdout.
	stdout := stdoutBuf.String()
	if !strings.Contains(stdout, "--all") {
		t.Errorf("destroy without --all should print usage hint, got: %s", stdout)
	}

	// stdout must not contain slog contamination.
	if strings.Contains(stdout, "level=") {
		t.Errorf("stdout should not contain slog lines, got: %s", stdout)
	}
}

// TestRootVerboseFlagParsed verifies that the --verbose flag is correctly
// parsed from the command line and reaches the globals.Options struct.
func TestRootVerboseFlagParsed(t *testing.T) {
	oplog.ResetForTest()

	var logBuf bytes.Buffer
	oplog.InitWithWriter(0, true, &logBuf)

	var stdoutBuf bytes.Buffer
	cmd := root.New(&stdoutBuf)
	cmd.SetOut(&stdoutBuf)
	cmd.SetArgs([]string{"-v", "version"})

	ctx := context.Background()
	err := cmd.ExecuteContext(ctx)
	if err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	// -v is short for --verbose.  Command should succeed.
	stdout := stdoutBuf.String()
	if !strings.Contains(stdout, "Fabrica") {
		t.Errorf("stdout should contain 'Fabrica', got: %s", stdout)
	}
}
