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

// loggerMu guards defaultLogger and initDone: ResetForTest may re-initialise
// the globals while a background goroutine (e.g. a lock heartbeat) is still
// logging through the With* helpers, so every access is synchronized.
var (
	loggerMu      sync.RWMutex
	defaultLogger *slog.Logger
	initDone      bool
)

func init() {
	defaultLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// applyInit installs a fresh logger writing to w, taking effect only on the
// first call until ResetForTest. Callers hold loggerMu.
func applyInit(level slog.Level, verbose bool, w io.Writer) {
	if initDone {
		return
	}
	initDone = true
	if verbose {
		level = slog.LevelDebug
	}
	defaultLogger = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
	}))
}

// Init configures the global logger from the given level and verbose flag.
//
// The level parameter should be an already-resolved slog.Level (typically from
// [ParseLevel]). When verbose is true, the level is overridden to debug.
//
// Init is safe to call multiple times; only the first call takes effect.
func Init(level slog.Level, verbose bool) {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	applyInit(level, verbose, os.Stderr)
}

// InitWithWriter is like Init but writes to the given writer instead of stderr.
// It is exported for testing only.
func InitWithWriter(level slog.Level, verbose bool, w io.Writer) {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	applyInit(level, verbose, w)
}

// ResetForTest resets the initDone guard so tests can re-initialise.
// Exported for testing only — never call in production code.
func ResetForTest() {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	initDone = false
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

// baseLogger returns the current global logger; the returned logger is
// immutable, so it stays usable after the lock is released.
func baseLogger() *slog.Logger {
	loggerMu.RLock()
	defer loggerMu.RUnlock()
	return defaultLogger
}

// Logger returns the global operational logger.
//
// This is safe to call before [Init] — it returns a default info-level
// logger to stderr.
func Logger() *slog.Logger {
	return baseLogger()
}

// WithModule returns a logger pre-bound with the module name attribute.
//
// Use this at the top of a command or operation so all subsequent log lines
// carry the module context without repeating it.
func WithModule(module string) *slog.Logger {
	return baseLogger().With(slog.String("module", module))
}

// WithResource returns a logger pre-bound with resource type and identifier.
func WithResource(typeName, id string) *slog.Logger {
	return baseLogger().With(
		slog.String("resource_type", typeName),
		slog.String("resource_id", id),
	)
}

// WithRegion returns a logger pre-bound with the AWS region.
func WithRegion(region string) *slog.Logger {
	return baseLogger().With(slog.String("region", region))
}

// WithResourceAndRegion returns a logger pre-bound with resource type, identifier, and region.
func WithResourceAndRegion(typeName, id, region string) *slog.Logger {
	return baseLogger().With(
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
