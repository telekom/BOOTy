package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{
		"": slog.LevelInfo, "info": slog.LevelInfo, "DEBUG": slog.LevelDebug,
		" warn ": slog.LevelWarn, "WARNING": slog.LevelWarn, "Error": slog.LevelError,
	} {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"trace", "info+2", "0"} {
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

func TestNewHandlerRedacts(t *testing.T) {
	for _, format := range []string{FormatText, FormatJSON} {
		var buf bytes.Buffer
		log := slog.New(NewHandler(&buf, format, slog.LevelDebug)).With("token", "with-secret")
		log.Debug("calling https://user:pass@example.com/x",
			"password", "hunter2",
			"cmd", "curl -H 'Authorization: Bearer abc.def.ghi'",
			slog.Group("auth", "user", "admin"),
			"error", errors.New("bad token=s3cr3t"),
			"disk", "/dev/sda", "count", 3)
		out := buf.String()
		for _, secret := range []string{"with-secret", "hunter2", "abc.def.ghi", "user:pass", "admin", "s3cr3t"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s: %q not redacted in %q", format, secret, out)
			}
		}
		if !strings.Contains(out, "/dev/sda") || !strings.Contains(out, "3") {
			t.Errorf("%s: plain values lost in %q", format, out)
		}
	}
}

// A map, struct or slice value is redacted by its keys and strings, not only
// by its rendered text, in both formats.
func TestNewHandlerRedactsComposites(t *testing.T) {
	type creds struct {
		User     string `json:"user"`
		Password string `json:"password"`
		Hidden   string `json:"-"`
		secret   string
	}
	plain := map[string]int{"disks": 2}
	for _, format := range []string{FormatText, FormatJSON} {
		var buf bytes.Buffer
		slog.New(NewHandler(&buf, format, slog.LevelDebug)).Info("composite",
			"detail", map[string]string{"text": "password=hunter2"},
			"nested", map[string]any{"list": []string{"ok", "token=s3cr3t"}},
			"creds", &creds{User: "admin", Password: "pw-value", Hidden: "hidden-value", secret: "unexported-value"},
			"plain", plain)
		out := buf.String()
		for _, secret := range []string{"hunter2", "s3cr3t", "pw-value", "hidden-value", "unexported-value"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s: %q not redacted in %q", format, secret, out)
			}
		}
		if !strings.Contains(out, "admin") || !strings.Contains(out, "disks") {
			t.Errorf("%s: plain values lost in %q", format, out)
		}
	}
}

// A nil slice or map keeps its text form: [] and map[], not <nil>.
func TestNewHandlerKeepsNilComposites(t *testing.T) {
	var buf bytes.Buffer
	var none []string
	var empty map[string]string
	slog.New(NewHandler(&buf, FormatText, slog.LevelInfo)).Info("nil", "list", none, "map", empty, "set", []string{})
	if out := buf.String(); !strings.Contains(out, "list=[]") || !strings.Contains(out, "map=map[]") || !strings.Contains(out, "set=[]") {
		t.Fatalf("text output = %q", out)
	}
}
