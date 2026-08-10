package oplog

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input  string
		expect slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{" info ", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"invalid", slog.LevelInfo},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := ParseLevel(tc.input)
			if got != tc.expect {
				t.Errorf("ParseLevel(%q) = %v, want %v", tc.input, got, tc.expect)
			}
		})
	}
}

func TestWithModule(t *testing.T) {
	var buf bytes.Buffer
	ResetForTest()
	InitWithWriter(slog.LevelInfo, false, &buf)

	l := WithModule("perforce")
	l.Info("test message")

	if !strings.Contains(buf.String(), `module=perforce`) {
		t.Fatalf("expected module=perforce in output, got: %s", buf.String())
	}
}

func TestWithResource(t *testing.T) {
	var buf bytes.Buffer
	ResetForTest()
	InitWithWriter(slog.LevelInfo, false, &buf)

	l := WithResource("AWS::EC2::Instance", "i-12345")
	l.Info("test message")

	output := buf.String()
	if !strings.Contains(output, `resource_type=AWS::EC2::Instance`) {
		t.Fatalf("expected resource_type in output, got: %s", output)
	}
	if !strings.Contains(output, `resource_id=i-12345`) {
		t.Fatalf("expected resource_id in output, got: %s", output)
	}
}

func TestWithRegion(t *testing.T) {
	var buf bytes.Buffer
	ResetForTest()
	InitWithWriter(slog.LevelInfo, false, &buf)

	l := WithRegion("us-east-1")
	l.Info("test message")

	if !strings.Contains(buf.String(), `region=us-east-1`) {
		t.Fatalf("expected region=us-east-1 in output, got: %s", buf.String())
	}
}

func TestRedact(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"", ""},
		{"secret123", "[redacted]"},
		{"aws-secret-access-key-value", "[redacted]"},
		{"my-userdata-script", "[redacted]"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := Redact(tc.input)
			if got != tc.expect {
				t.Errorf("Redact(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestRedactValue(t *testing.T) {
	var buf bytes.Buffer
	ResetForTest()
	InitWithWriter(slog.LevelInfo, false, &buf)

	Logger().Info("secret log", "password", RedactValue("super-secret"))

	output := buf.String()
	if strings.Contains(output, "super-secret") {
		t.Fatalf("secret should be redacted in output, got: %s", output)
	}
	if !strings.Contains(output, "[redacted]") {
		t.Fatalf("expected [redacted] in output, got: %s", output)
	}
}

func TestSafeWithoutInit(t *testing.T) {
	ResetForTest()
	// Logger() should not panic when Init has not been called.
	l := Logger()
	if l == nil {
		t.Fatal("Logger() returned nil without Init")
	}
}

func TestLevelGating(t *testing.T) {
	var buf bytes.Buffer
	ResetForTest()
	InitWithWriter(slog.LevelInfo, false, &buf)

	Logger().Debug("debug line")
	Logger().Info("info line")
	Logger().Warn("warn line")
	Logger().Error("error line")

	output := buf.String()
	if strings.Contains(output, "debug line") {
		t.Error("debug should not appear at info level")
	}
	if !strings.Contains(output, "info line") {
		t.Error("info should appear at info level")
	}
	if !strings.Contains(output, "warn line") {
		t.Error("warn should appear at info level")
	}
	if !strings.Contains(output, "error line") {
		t.Error("error should appear at info level")
	}
}

func TestVerboseLevelAllowsDebug(t *testing.T) {
	var buf bytes.Buffer
	ResetForTest()
	InitWithWriter(slog.LevelInfo, true, &buf) // verbose=true forces debug

	Logger().Debug("debug visible")
	Logger().Info("info visible")

	output := buf.String()
	if !strings.Contains(output, "debug visible") {
		t.Error("debug should appear when verbose=true")
	}
	if !strings.Contains(output, "info visible") {
		t.Error("info should appear when verbose=true")
	}
}

func TestInitIdempotent(t *testing.T) {
	var buf bytes.Buffer
	ResetForTest()
	InitWithWriter(slog.LevelDebug, true, &buf)

	// Second init should be ignored (initOnce guards it).
	var buf2 bytes.Buffer
	InitWithWriter(slog.LevelError, false, &buf2)

	Logger().Debug("should still appear")
	if !strings.Contains(buf.String(), "should still appear") {
		t.Fatal("second Init should not change level due to initOnce guard")
	}
}

func TestLoggerNotNil(t *testing.T) {
	l := Logger()
	if l == nil {
		t.Fatal("Logger() returned nil")
	}
}

func TestDefaultQuiet(t *testing.T) {
	// Without verbose, debug lines should not appear.
	var buf bytes.Buffer
	ResetForTest()
	InitWithWriter(slog.LevelInfo, false, &buf)

	Logger().Debug("should not appear")
	Logger().Info("should appear")

	output := buf.String()
	if strings.Contains(output, "should not appear") {
		t.Error("debug should not appear without verbose")
	}
	if !strings.Contains(output, "should appear") {
		t.Error("info should appear without verbose")
	}
}

func TestInitWithVerbose(t *testing.T) {
	ResetForTest()
	Init(slog.LevelInfo, true) // verbose forces debug

	Logger().Debug("init verbose test")
	if Logger() == nil {
		t.Fatal("Logger() is nil after Init")
	}
}

func TestInitWithLevel(t *testing.T) {
	ResetForTest()
	Init(slog.LevelWarn, false)

	// Verify the logger is non-nil and doesn't panic.
	Logger().Warn("init level test")
	if Logger() == nil {
		t.Fatal("Logger() is nil after Init")
	}
}

func TestInitExplicitInfoLevel(t *testing.T) {
	ResetForTest()
	Init(slog.LevelInfo, false)

	Logger().Info("init info level test")
	if Logger() == nil {
		t.Fatal("Logger() is nil after Init")
	}
}

func TestWithResourceAndRegion(t *testing.T) {
	var buf bytes.Buffer
	ResetForTest()
	InitWithWriter(slog.LevelInfo, true, &buf)

	WithResourceAndRegion("AWS::EC2::Instance", "i-123", "us-east-1").Info("test message")

	output := buf.String()
	if !strings.Contains(output, "resource_type=AWS::EC2::Instance") {
		t.Errorf("expected resource_type in output, got: %s", output)
	}
	if !strings.Contains(output, "resource_id=i-123") {
		t.Errorf("expected resource_id in output, got: %s", output)
	}
	if !strings.Contains(output, "region=us-east-1") {
		t.Errorf("expected region in output, got: %s", output)
	}
}
