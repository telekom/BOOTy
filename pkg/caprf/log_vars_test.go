package caprf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVarsLogKeys(t *testing.T) {
	cfg, err := ParseVars(strings.NewReader("LOG_LEVEL=debug\nLOG_FORMAT=JSON\n"))
	if err != nil {
		t.Fatalf("ParseVars() = %v", err)
	}
	if cfg.Transport.LogLevel != "debug" || cfg.Transport.LogFormat != "JSON" {
		t.Fatalf("LogLevel = %q, LogFormat = %q", cfg.Transport.LogLevel, cfg.Transport.LogFormat)
	}
	for _, line := range []string{"LOG_LEVEL=trace\n", "LOG_FORMAT=console\n"} {
		if _, err := ParseVars(strings.NewReader(line)); err == nil {
			t.Errorf("ParseVars(%q) accepted an invalid value", line)
		}
	}
}

func TestFailureConfigReadsLogKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vars")
	// The file does not parse (LUKS_ key), the log keys still apply.
	if err := os.WriteFile(path, []byte("LOG_LEVEL='warn'\nLUKS_ENABLED=true\nexport LOG_FORMAT=\"json\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := FailureConfig(path)
	if cfg.Transport.LogLevel != "warn" || cfg.Transport.LogFormat != "json" {
		t.Fatalf("LogLevel = %q, LogFormat = %q", cfg.Transport.LogLevel, cfg.Transport.LogFormat)
	}
}
