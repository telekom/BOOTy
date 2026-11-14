//go:build linux

package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupLogging(t *testing.T) {
	orig := slog.Default()
	t.Cleanup(func() { slog.SetDefault(orig); logLevel.Set(slog.LevelInfo) })

	setupLogging("", "")
	if logLevel.Level() != slog.LevelInfo {
		t.Fatalf("default level = %v, want INFO", logLevel.Level())
	}
	if _, ok := slog.Default().Handler().(*slog.TextHandler); !ok {
		t.Fatalf("default handler = %T, want text", slog.Default().Handler())
	}

	setupLogging("debug", "json")
	if _, ok := slog.Default().Handler().(*slog.JSONHandler); !ok || logLevel.Level() != slog.LevelDebug {
		t.Fatalf("handler = %T level = %v, want JSON DEBUG", slog.Default().Handler(), logLevel.Level())
	}

	// A logger kept from before follows a later level change.
	kept := slog.Default()
	setupLogging("bogus", "bogus")
	if logLevel.Level() != slog.LevelInfo || kept.Enabled(t.Context(), slog.LevelDebug) {
		t.Fatal("an invalid level did not fall back to info for every logger")
	}
}

func TestSetupLoggingFromVars(t *testing.T) {
	orig := slog.Default()
	t.Cleanup(func() { slog.SetDefault(orig); logLevel.Set(slog.LevelInfo) })
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("LOG_FORMAT", "")

	vars := filepath.Join(t.TempDir(), "vars")
	setupLogging("error", "")
	if err := os.WriteFile(vars, []byte("MODE=provision\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setupLoggingFromVars(vars)
	if logLevel.Level() != slog.LevelError {
		t.Fatalf("vars without log keys changed the level to %v", logLevel.Level())
	}

	// The vars format applies; the level stays from the environment.
	if err := os.WriteFile(vars, []byte("export LOG_FORMAT=\"json\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setupLoggingFromVars(vars)
	if _, ok := slog.Default().Handler().(*slog.JSONHandler); !ok || logLevel.Level() != slog.LevelError {
		t.Fatalf("handler = %T level = %v, want JSON ERROR", slog.Default().Handler(), logLevel.Level())
	}

	if err := os.WriteFile(vars, []byte("LOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setupLoggingFromVars(vars)
	if logLevel.Level() != slog.LevelDebug {
		t.Fatalf("vars LOG_LEVEL=debug gave %v", logLevel.Level())
	}
}

// An invalid LOG_FORMAT is reported with LOG_LEVEL=error as well.
func TestSetupLoggingWarnsAboveLevel(t *testing.T) {
	orig, origStderr := slog.Default(), os.Stderr
	t.Cleanup(func() { slog.SetDefault(orig); logLevel.Set(slog.LevelInfo); os.Stderr = origStderr })
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = f
	setupLogging("error", "bogus")
	os.Stderr = origStderr
	_ = f.Close()
	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "ignoring log setting") || !strings.Contains(string(out), "bogus") {
		t.Fatalf("no warning for LOG_FORMAT=bogus with LOG_LEVEL=error: %q", out)
	}
}

// An empty vars entry resets the setting to the default instead of keeping
// the environment value.
func TestSetupLoggingFromVarsEmptyResets(t *testing.T) {
	orig := slog.Default()
	t.Cleanup(func() { slog.SetDefault(orig); logLevel.Set(slog.LevelInfo) })
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "json")

	vars := filepath.Join(t.TempDir(), "vars")
	if err := os.WriteFile(vars, []byte("LOG_LEVEL=\nLOG_FORMAT=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setupLogging("debug", "json")
	setupLoggingFromVars(vars)
	if _, ok := slog.Default().Handler().(*slog.TextHandler); !ok || logLevel.Level() != slog.LevelInfo {
		t.Fatalf("handler = %T level = %v, want text INFO", slog.Default().Handler(), logLevel.Level())
	}
}
