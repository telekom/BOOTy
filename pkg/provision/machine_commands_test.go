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

func TestCompareMachineCommandNamesUsesCAPRFIndex(t *testing.T) {
	names := []string{"commands-10.sh", "zz", "commands-2.sh", "commands-0.sh", "aa", "commands-1.sh"}
	slices.SortStableFunc(names, compareMachineCommandNames)
	want := []string{"commands-0.sh", "commands-1.sh", "commands-2.sh", "commands-10.sh", "aa", "zz"}
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
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)

	if err := c.RunMachineCommands(context.Background(), true); err != nil {
		t.Fatalf("RunMachineCommands() = %v", err)
	}

	got := make([][]string, 0, len(cmd.calls))
	for _, call := range cmd.calls {
		got = append(got, append([]string{call.name}, call.args...))
	}
	want := [][]string{
		{"chroot", c.rootDir, "/bin/sh", "/tmp/booty-machine-commands/commands-2.sh"},
		{"chroot", c.rootDir, "/tmp/booty-machine-commands/commands-10.sh"},
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
	cmd := newMockCommander()
	c := newTestConfigurator(t, cmd)
	cmd.setResult("chroot "+c.rootDir, []byte("boom\n"), errors.New("exit status 1"))

	err := c.RunMachineCommands(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "commands-0.sh") {
		t.Fatalf("RunMachineCommands() = %v, want commands-0.sh failure", err)
	}
	if len(cmd.calls) != 1 {
		t.Fatalf("calls = %d, want the run to stop after the failing command", len(cmd.calls))
	}
}
