//go:build linux

package main

import (
	"cmp"
	"log/slog"
	"os"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/logging"
)

// logLevel is the level of the console handler and of the LOG_URL handler.
// The loggers that packages keep from slog.Default() share it, so a level
// from the vars file applies to them too.
var logLevel slog.LevelVar

// setupLogging installs the console handler (stderr, which is /dev/console
// for PID 1) for LOG_LEVEL and LOG_FORMAT. An invalid value falls back to
// the default (info, text) and logs a warning.
func setupLogging(level, format string) {
	l, levelErr := logging.ParseLevel(level)
	f, formatErr := logging.ParseFormat(format)
	logLevel.Set(l)
	slog.SetDefault(slog.New(logging.NewHandler(os.Stderr, f, &logLevel)))
	for _, err := range []error{levelErr, formatErr} {
		if err != nil {
			slog.Warn("ignoring log setting", "error", err)
		}
	}
}

// setupLoggingFromVars applies LOG_LEVEL and LOG_FORMAT of the vars file,
// before the full parse so that the CAPRF client logger uses them too. A
// vars entry wins over the process environment.
func setupLoggingFromVars(varsPath string) {
	t := caprf.FailureConfig(varsPath).Transport
	if t.LogLevel == "" && t.LogFormat == "" {
		return
	}
	setupLogging(cmp.Or(t.LogLevel, os.Getenv("LOG_LEVEL")), cmp.Or(t.LogFormat, os.Getenv("LOG_FORMAT")))
}
