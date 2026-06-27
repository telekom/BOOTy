package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMakefileOCIPushTargetsCheckRefBeforePush(t *testing.T) {
	data, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(data)
	if !strings.Contains(makefile, `oras manifest fetch "$$ref"`) {
		t.Fatal("Makefile OCI guard must probe the remote ref before publishing")
	}

	tests := []struct {
		name  string
		start string
		guard string
		push  string
	}{
		{
			name:  "initramfs",
			start: "oci-push-initramfs:",
			guard: "$(call ensure_oci_ref_absent,$${REPOSITORY}/initramfs:" +
				"$${DOCKERTAG}-$${OCI_FLAVOR}-$${OCI_ARCH})",
			push: `@oras push "$${REPOSITORY}/initramfs:` +
				`$${DOCKERTAG}-$${OCI_FLAVOR}-$${OCI_ARCH}"`,
		},
		{
			name:  "binary",
			start: "oci-push-binary:",
			guard: "$(call ensure_oci_ref_absent,$${REPOSITORY}/binary:" +
				"$${DOCKERTAG}-$${OCI_ARCH})",
			push: `@oras push $(REPOSITORY)/binary:$(DOCKERTAG)-$(OCI_ARCH)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetStart := strings.Index(makefile, tt.start)
			if targetStart < 0 {
				t.Fatalf("target %q not found", tt.start)
			}
			guardIndex := strings.Index(makefile[targetStart:], tt.guard)
			if guardIndex < 0 {
				t.Fatalf("%s target missing OCI absence guard", tt.name)
			}
			pushIndex := strings.Index(makefile[targetStart:], tt.push)
			if pushIndex < 0 {
				t.Fatalf("%s target missing oras push", tt.name)
			}
			if guardIndex > pushIndex {
				t.Fatalf("%s target checks OCI ref after oras push", tt.name)
			}
		})
	}
}

func TestOCIPushTargetsStopBeforePushWhenRefExists(t *testing.T) {
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skipf("make not available: %v", err)
	}

	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	orasPath := filepath.Join(fakeBin, "oras")
	orasScript := `#!/bin/sh
printf '%s\n' "$*" >> "$ORAS_LOG"
case "$1 $2" in
  "manifest fetch")
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`
	if err := os.WriteFile(orasPath, []byte(orasScript), 0o755); err != nil {
		t.Fatalf("write fake oras: %v", err)
	}

	initramfs := filepath.Join(tmp, "initramfs.cpio.zst")
	binary := filepath.Join(tmp, "booty")
	for _, path := range []string{initramfs, binary} {
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	tests := []struct {
		name   string
		target string
		args   []string
		ref    string
	}{
		{
			name:   "initramfs",
			target: "oci-push-initramfs",
			args: []string{
				"INITRAMFS_PATH=" + initramfs,
				"OCI_FLAVOR=default",
			},
			ref: "example.invalid/booty/initramfs:test-default-amd64",
		},
		{
			name:   "binary",
			target: "oci-push-binary",
			args: []string{
				"TARGET=" + binary,
			},
			ref: "example.invalid/booty/binary:test-amd64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logPath := filepath.Join(tmp, tt.name+".log")
			args := make([]string, 0, 4+len(tt.args))
			args = append(args,
				tt.target,
				"REPOSITORY=example.invalid/booty",
				"DOCKERTAG=test",
				"OCI_ARCH=amd64",
			)
			args = append(args, tt.args...)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, makePath, args...)
			cmd.Env = append(os.Environ(),
				"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"ORAS_LOG="+logPath,
			)
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("make %s timed out: %v\n%s", tt.target, ctx.Err(), out)
			}
			if err == nil {
				t.Fatalf("make %s succeeded; want overwrite guard failure\n%s", tt.target, out)
			}
			if !strings.Contains(string(out), "already exists") {
				t.Fatalf("make %s output = %q, want overwrite error", tt.target, out)
			}
			logData, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read fake oras log: %v", err)
			}
			log := string(logData)
			if !strings.Contains(log, "manifest fetch "+tt.ref) {
				t.Fatalf("oras log = %q, want manifest fetch for %s", log, tt.ref)
			}
			if strings.Contains(log, "push ") {
				t.Fatalf("oras push was reached despite existing ref: %q", log)
			}
		})
	}
}
