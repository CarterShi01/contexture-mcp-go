package server

import (
	"fmt"
	"log/slog"
	"os"
)

// LogLevel controls Contexture lifecycle records written by a Host adapter.
type LogLevel string

const (
	DebugLogLevel LogLevel = "debug"
	InfoLogLevel  LogLevel = "info"
	WarnLogLevel  LogLevel = "warn"
	ErrorLogLevel LogLevel = "error"
)

// ConfigureLogging installs Contexture's process-wide structured logger on
// stderr. Stdio transports reserve stdout exclusively for MCP framing.
func ConfigureLogging(level LogLevel) error {
	resolved, ok := slogLevel(level)
	if !ok {
		return &ServeError{Message: fmt.Sprintf("unknown Contexture log level %q", level)}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: resolved})))
	return nil
}

func slogLevel(level LogLevel) (slog.Level, bool) {
	switch level {
	case DebugLogLevel:
		return slog.LevelDebug, true
	case InfoLogLevel:
		return slog.LevelInfo, true
	case WarnLogLevel:
		return slog.LevelWarn, true
	case ErrorLogLevel:
		return slog.LevelError, true
	default:
		return 0, false
	}
}
