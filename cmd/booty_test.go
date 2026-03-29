package cmd

import (
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer func() { _ = r.Close() }()

	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}

	return string(data)
}

func TestBootyVersionCommandOutput(t *testing.T) {
	orig := Release
	defer func() { Release = orig }()

	Release.Version = "v1.2.3"
	Release.Build = "abc123"

	out := captureStdout(t, func() {
		bootyVersion.Run(bootyVersion, nil)
	})

	if !strings.Contains(out, "BOOTy Release Information") {
		t.Fatalf("missing release header in output: %q", out)
	}
	if !strings.Contains(out, "Version:  v1.2.3") {
		t.Fatalf("missing version in output: %q", out)
	}
	if !strings.Contains(out, "Build:    abc123") {
		t.Fatalf("missing build in output: %q", out)
	}
}

func TestBootyCommandHasVersionSubcommand(t *testing.T) {
	var found bool
	for _, c := range bootyCmd.Commands() {
		if c.Use == "version" {
			found = true
			break
		}
	}

	if !found {
		t.Fatal("version subcommand not registered")
	}
}
