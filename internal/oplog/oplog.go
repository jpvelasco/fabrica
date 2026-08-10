// Package oplog provides operational logging for Fabrica using the Go stdlib
// log/slog package. It is the only place that configures log handlers and levels.
//
// Command and domain code must call oplog helpers — never slog directly.
//
// Logs go to stderr so that stdout remains pure for --json machine output.
// Default level is info (quiet for happy-path runs). Enable debug-level
// diagnostics with --verbose or FABRICA_LOG_LEVEL=debug.
//
// Secrets (passwords, tokens, raw UserData, IAM policy documents) must never
// be logged. Use [Redact] for values that might contain sensitive material.
package oplog

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

var (
	defaultLogger *slog.Logger
	initOnce      sync.Once
)

func init() {
	defaultLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// Init configures the global logger from the given level and verbose flag.
//
// When verbose is true, the level is set to debug. When verbose is false,
// the level comes from the level parameter (typically parsed from
// FABRICA_LOG_LEVEL). A zero/empty level defaults to info.
//
// Init is safe to call multiple times; only the first call takes effect.
func Init(level slog.Level, verbose bool) {
	initOnce.Do(func() {
		if verbose {
			level = slog.LevelDebug
		}
		if level == 0 {
			level = slog.LevelInfo
		}
		defaultLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: level,
		}))
	})
}

// InitWithWriter is like Init but writes to the given writer instead of stderr.
// It is exported for testing only.
func InitWithWriter(level slog.Level, verbose bool, w io.Writer) {
	initOnce.Do(func() {
		if verbose {
			level = slog.LevelDebug
		}
		if level == 0 {
			level = slog.LevelInfo
		}
		defaultLogger = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
			Level: level,
		}))
	})
}

// ResetForTest resets the initOnce guard so tests can re-initialise.
// Exported for testing only — never call in production code.
func ResetForTest() {
	initOnce = sync.Once{}
	defaultLogger = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// ParseLevel returns an slog.Level from a string, or the default level if
// the string is empty or unrecognised.
//
// Recognised values (case-insensitive): debug, info, warn, error.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Logger returns the global operational logger.
//
// This is safe to call before [Init] — it returns a default info-level
// logger to stderr.
func Logger() *slog.Logger {
	return defaultLogger
}

// WithModule returns a logger pre-bound with the module name attribute.
//
// Use this at the top of a command or operation so all subsequent log lines
// carry the module context without repeating it.
func WithModule(module string) *slog.Logger {
	return defaultLogger.With(slog.String("module", module))
}

// WithResource returns a logger pre-bound with resource type and identifier.
func WithResource(typeName, id string) *slog.Logger {
	return defaultLogger.With(
		slog.String("resource_type", typeName),
		slog.String("resource_id", id),
	)
}

// WithRegion returns a logger pre-bound with the AWS region.
func WithRegion(region string) *slog.Logger {
	return defaultLogger.With(slog.String("region", region))
}

// WithResourceAndRegion returns a logger pre-bound with resource type, identifier, and region.
func WithResourceAndRegion(typeName, id, region string) *slog.Logger {
	return defaultLogger.With(
		slog.String("resource_type", typeName),
		slog.String("resource_id", id),
		slog.String("region", region),
	)
}

// Redact replaces any non-empty value with "[redacted]".
//
// Use this for attributes that may carry secrets (passwords, tokens,
// raw UserData, IAM policy documents). An empty string is returned as-is.
func Redact(s string) string {
	if s == "" {
		return s
	}
	return "[redacted]"
}

// RedactValue returns an slog.Value that is redacted if the input is non-empty.
func RedactValue(s string) slog.Value {
	return slog.StringValue(Redact(s))
}
