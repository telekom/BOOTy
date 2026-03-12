//go:build linux

package realm

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
)

// Shell will Start a userland shell.
func Shell() {
	slog.Info("Starting Shell")

	cmd := exec.CommandContext(context.Background(), "/usr/bin/setsid", "cttyhack", "/bin/sh")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	err := cmd.Start()
	if err != nil {
		slog.Error("Shell error", "error", err)
	}
	slog.Info("Waiting for command to finish...")
	err = cmd.Wait()
	if err != nil {
		slog.Error("Shell error", "error", err)
	}
}
