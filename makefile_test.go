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
		{name: "version", arg: "VERSION=v1;touch", want: "invalid VERSION"},
		{name: "build", arg: "BUILD=abc;touch", want: "invalid BUILD"},
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

func TestMakefileRejectsUnsafeOCISelectors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		arg  string
		want string
	}{
		{name: "flavor", arg: "OCI_FLAVOR=default;touch", want: "invalid OCI_FLAVOR"},
		{name: "arch", arg: "OCI_ARCH=amd64;touch", want: "invalid OCI_ARCH"},
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
