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

func TestMakefileRejectsUnsafeDockerTag(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "makefile-injection-marker")
	payload := "v1; touch " + marker
	output, err := runMake(t, "check-docker-vars", "DOCKERTAG="+payload)
	if err == nil {
		t.Fatalf("check-docker-vars accepted unsafe DOCKERTAG; output:\n%s", output)
	}
	if !strings.Contains(string(output), "invalid DOCKERTAG") {
		t.Fatalf("check-docker-vars output = %q, want invalid DOCKERTAG", output)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe DOCKERTAG payload created marker file: %v", statErr)
	}
}

func TestMakefileRejectsUnsafeBuildVariables(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		arg  string
		want string
	}{
		{name: "target", arg: "TARGET=booty;touch", want: "invalid TARGET"},
		{name: "target-leading-dash", arg: "TARGET=-booty", want: "invalid TARGET"},
		{name: "target-multiline", arg: "TARGET=../bad\nbooty", want: "invalid TARGET"},
		{name: "version", arg: "VERSION=v1;touch", want: "invalid VERSION"},
		{name: "version-multiline", arg: "VERSION=bad;touch\nv1", want: "invalid VERSION"},
		{name: "build", arg: "BUILD=abc;touch", want: "invalid BUILD"},
		{name: "build-multiline", arg: "BUILD=bad;touch\nabcdef1", want: "invalid BUILD"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			output, err := runMake(t, "check-build-vars", tc.arg)
			if err == nil {
				t.Fatalf("check-build-vars accepted unsafe variable %q; output:\n%s", tc.arg, output)
			}
			if !strings.Contains(string(output), tc.want) {
				t.Fatalf("check-build-vars output = %q, want %q", output, tc.want)
			}
		})
	}
}

func TestMakefileAllowsPathLikeTarget(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "booty binary")
	output, err := runMake(t, "check-build-vars", "TARGET="+target)
	if err != nil {
		t.Fatalf("check-build-vars rejected path-like TARGET %q: %v\n%s", target, err, output)
	}
}

func TestMakefileRejectsTraversalTarget(t *testing.T) {
	t.Parallel()

	output, err := runMake(t, "check-build-vars", "TARGET=../booty")
	if err == nil {
		t.Fatalf("check-build-vars accepted traversal TARGET; output:\n%s", output)
	}
	if !strings.Contains(string(output), "invalid TARGET") {
		t.Fatalf("check-build-vars output = %q, want invalid TARGET", output)
	}
}

func TestMakefileBuildSkipsUpToDateBinary(t *testing.T) {
	t.Parallel()

	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skipf("make not found on PATH: %v", err)
	}
	repoDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.Mkdir(fakeBin, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	writeExecutable(t, filepath.Join(fakeBin, "go"), `#!/bin/sh
printf '%s\n' "$*" >> "$GO_LOG"
if [ "$1" = build ]; then
  out=
  while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then
      shift
      out=$1
    fi
    shift || break
  done
  if [ -z "$out" ]; then
    printf '%s\n' 'missing -o output' >&2
    exit 1
  fi
  printf '%s' payload > "$out"
fi
`)

	logPath := filepath.Join(tmp, "go.log")
	target := filepath.Join(tmp, "booty")
	args := []string{
		"-f", filepath.Join(repoDir, "Makefile"),
		"build",
		"TARGET=" + target,
		"TARGETOS=linux",
		"TARGETARCH=amd64",
		"VERSION=test",
		"BUILD=abcdef1",
	}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, makePath, args...)
		cmd.Dir = tmp
		cmd.Env = append(os.Environ(),
			"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
			"GO_LOG="+logPath,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			cancel()
			t.Fatalf("make build run %d failed: %v\n%s", i+1, err, out)
		}
		cancel()
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake go log: %v", err)
	}
	if got := strings.Count(string(logData), "build "); got != 1 {
		t.Fatalf("go build calls = %d, want 1; log:\n%s", got, logData)
	}
}

func TestMakefileRejectsUnsafeTargetForDestructiveTargets(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"clean", "uninstall"} {
		target := target
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			marker := filepath.Join(t.TempDir(), "makefile-target-injection-marker")
			payload := "booty; touch " + marker
			output, err := runMake(t, target, "TARGET="+payload)
			if err == nil {
				t.Fatalf("%s accepted unsafe TARGET; output:\n%s", target, output)
			}
			if !strings.Contains(string(output), "invalid TARGET") {
				t.Fatalf("%s output = %q, want invalid TARGET", target, output)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("unsafe TARGET payload for %s created marker file: %v", target, statErr)
			}
		})
	}
}

func TestMakefileRejectsMultilineDockerVariables(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		arg  string
		want string
	}{
		{name: "repository", arg: "REPOSITORY=bad;touch\nghcr.io/telekom/booty", want: "invalid REPOSITORY"},
		{name: "dockertag", arg: "DOCKERTAG=bad;touch\nv1", want: "invalid DOCKERTAG"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			output, err := runMake(t, "check-docker-vars", tc.arg)
			if err == nil {
				t.Fatalf("check-docker-vars accepted multi-line variable %q; output:\n%s", tc.arg, output)
			}
			if !strings.Contains(string(output), tc.want) {
				t.Fatalf("check-docker-vars output = %q, want %q", output, tc.want)
			}
		})
	}
}

func TestMakefileRejectsUnsafeOCISelectors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		arg  string
		want string
	}{
		{name: "flavor", arg: "OCI_FLAVOR=default;touch", want: "invalid OCI_FLAVOR"},
		{name: "flavor-multiline", arg: "OCI_FLAVOR=bad;touch\ndefault", want: "invalid OCI_FLAVOR"},
		{name: "arch", arg: "OCI_ARCH=amd64;touch", want: "invalid OCI_ARCH"},
		{name: "arch-multiline", arg: "OCI_ARCH=bad;touch\namd64", want: "invalid OCI_ARCH"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			output, err := runMake(t, "check-oci-vars", tc.arg)
			if err == nil {
				t.Fatalf("check-oci-vars accepted unsafe selector %q; output:\n%s", tc.arg, output)
			}
			if !strings.Contains(string(output), tc.want) {
				t.Fatalf("check-oci-vars output = %q, want %q", output, tc.want)
			}
		})
	}
}

func runMake(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skipf("make not found on PATH: %v", err)
	}

	cmdArgs := append([]string{"-s"}, args...)
	return exec.CommandContext(ctx, makePath, cmdArgs...).CombinedOutput()
}

func TestMakefileUsesQuotedShellVariablesForPublishRefs(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatalf("cannot read Makefile: %v", err)
	}
	makefile := string(data)

	for _, forbidden := range []string{
		"$(REPOSITORY):$(DOCKERTAG)",
		"$(REPOSITORY)/initramfs:$(DOCKERTAG)",
		"$(REPOSITORY)/binary:$(DOCKERTAG)",
		"$(INITRAMFS_PATH):$(INITRAMFS_MEDIA_TYPE)",
		"$(TARGET):application/vnd.cncf.binary.layer.v1",
	} {
		if strings.Contains(makefile, forbidden) {
			t.Fatalf("Makefile still embeds unquoted make expansion %q in shell recipes", forbidden)
		}
	}

	for _, required := range []string{
		`"$${REPOSITORY}:$${DOCKERTAG}"`,
		`"$${REPOSITORY}/initramfs:$${DOCKERTAG}-$${OCI_FLAVOR}-$${OCI_ARCH}"`,
		`"$${REPOSITORY}/binary:$${DOCKERTAG}-$${OCI_ARCH}"`,
		`"$${INITRAMFS_PATH}:$${INITRAMFS_MEDIA_TYPE}"`,
		`"$${TARGET}:application/vnd.cncf.binary.layer.v1"`,
	} {
		if !strings.Contains(makefile, required) {
			t.Fatalf("Makefile missing quoted shell expansion %q", required)
		}
	}
}
