package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{
		"": slog.LevelInfo, "info": slog.LevelInfo, "DEBUG": slog.LevelDebug,
		" warn ": slog.LevelWarn, "Error": slog.LevelError,
	} {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"trace", "warning", "info+2", "0"} {
		if _, err := ParseLevel(in); err == nil {
			t.Errorf("ParseLevel(%q) accepted an invalid level", in)
		}
	}
}

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]string{"": FormatText, "TEXT": FormatText, "json": FormatJSON} {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseFormat("console"); err == nil {
		t.Error("ParseFormat accepted console")
	}
}

func TestNewHandler(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(NewHandler(&buf, FormatJSON, slog.LevelWarn))
	log.Info("dropped")
	log.Warn("kept", "disk", "/dev/sda")
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("JSON output %q: %v", buf.String(), err)
	}
	if line["level"] != "WARN" || line["msg"] != "kept" || line["disk"] != "/dev/sda" || line["time"] == nil {
		t.Fatalf("JSON line = %v", line)
	}

	buf.Reset()
	var level slog.LevelVar
	log = slog.New(NewHandler(&buf, FormatText, &level))
	log.Debug("hidden")
	level.Set(slog.LevelDebug)
	log.Debug("shown")
	if out := buf.String(); strings.Contains(out, "hidden") || !strings.Contains(out, "level=DEBUG msg=shown") {
		t.Fatalf("text output = %q", out)
	}
}
