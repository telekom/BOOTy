//go:build linux

package provision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func withMachineCommands(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	old := machineCommandsDir
	machineCommandsDir = dir
	t.Cleanup(func() { machineCommandsDir = old })
}

// fakeStreaming replaces runStreamingCommand. It records each command and
// emits the given output lines.
type fakeStreaming struct {
	calls  [][]string
	output []string
	err    error
}

func withFakeStreaming(t *testing.T, f *fakeStreaming) {
	t.Helper()
	old := runStreamingCommand
	runStreamingCommand = func(_ context.Context, onLine func(string), root string, argv []string) error {
		f.calls = append(f.calls, append([]string{root}, argv...))
		for _, line := range f.output {
			onLine(line)
		}
		return f.err
	}
	t.Cleanup(func() { runStreamingCommand = old })
}

func TestCompareMachineCommandNamesUsesCAPRFIndex(t *testing.T) {
	names := []string{"commands-10.sh", "zz", "commands-+1.sh", "commands-2.sh", "commands-0.sh", "aa", "commands-1.sh"}
	slices.SortStableFunc(names, compareMachineCommandNames)
	want := []string{"commands-0.sh", "commands-1.sh", "commands-2.sh", "commands-10.sh", "aa", "commands-+1.sh", "zz"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
}

// Without legacy compat, BOOTy keeps rejecting shell metacharacters.
func TestRunMachineCommandsDefaultRejectsShell(t *testing.T) {
	withMachineCommands(t, map[string]string{"commands-0.sh": "echo a > /etc/a"})
	c := newTestConfigurator(t, newMockCommander())
	err := c.RunMachineCommands(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("RunMachineCommands() = %v, want rejected command", err)
	}
}

// Legacy evidence: provision.sh runs every machine-commands file with
// run_script_in_chroot, which copies the file into the target root and
// executes it there. Files without "#!" run with /bin/sh.
func TestRunMachineCommandsLegacyCompatRunsScripts(t *testing.T) {
	script := "set -e\nprintf '%s\\n' \"$(hostname)\" > /etc/legacy\nsystemctl enable foo | tee /dev/null\n"
	withMachineCommands(t, map[string]string{
		"commands-10.sh": "#!/bin/bash\necho ten\n",
		"commands-2.sh":  script,
		"commands-3.sh":  "  \n",
	})
	fake := &fakeStreaming{}
	withFakeStreaming(t, fake)
	c := newTestConfigurator(t, newMockCommander())

	if err := c.RunMachineCommands(context.Background(), true); err != nil {
		t.Fatalf("RunMachineCommands() = %v", err)
	}

	got := fake.calls
	want := [][]string{
		{c.rootDir, "/bin/sh", "/tmp/booty-machine-commands/commands-2.sh"},
		{c.rootDir, "/tmp/booty-machine-commands/commands-10.sh"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for _, name := range []string{"commands-2.sh", "commands-10.sh"} {
		if _, err := os.Stat(filepath.Join(c.rootDir, "tmp", "booty-machine-commands", name)); !os.IsNotExist(err) {
			t.Errorf("staged script %s not removed: %v", name, err)
		}
	}
}

func TestRunMachineCommandsLegacyCompatFailureStopsProvisioning(t *testing.T) {
	withMachineCommands(t, map[string]string{"commands-0.sh": "false\n", "commands-1.sh": "true\n"})
	fake := &fakeStreaming{output: []string{"boom"}, err: errors.New("run chroot: exit status 1 token=secret")}
	withFakeStreaming(t, fake)
	c := newTestConfigurator(t, newMockCommander())

	err := c.RunMachineCommands(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "commands-0.sh") {
		t.Fatalf("RunMachineCommands() = %v, want commands-0.sh failure", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("RunMachineCommands() error leaks a secret: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls = %d, want the run to stop after the failing command", len(fake.calls))
	}
}

// A target image must not redirect the staged script through a symlink.
func TestRunMachineCommandsLegacyCompatReplacesStagingSymlink(t *testing.T) {
	withMachineCommands(t, map[string]string{"commands-0.sh": "true\n"})
	withFakeStreaming(t, &fakeStreaming{})
	c := newTestConfigurator(t, newMockCommander())
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(c.rootDir, "tmp", "booty-machine-commands")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(staging, "commands-0.sh")); err != nil {
		t.Fatal(err)
	}

	if err := c.RunMachineCommands(context.Background(), true); err != nil {
		t.Fatalf("RunMachineCommands() = %v", err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "keep\n" {
		t.Fatalf("file behind the symlink changed: %q, %v", got, err)
	}
}
