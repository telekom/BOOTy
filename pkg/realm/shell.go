//go:build linux

package realm

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
)

// Shell starts a userland shell. It uses BusyBox setsid + cttyhack to
// attach the shell to the controlling terminal so interactive input works.
// Paths use /bin/ because BusyBox applets are installed there in the initramfs.
func Shell() {
	slog.Info("starting Shell")

	cmd := exec.CommandContext(context.Background(), "/bin/setsid", "cttyhack", "/bin/sh")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	err := cmd.Start()
	if err != nil {
		slog.Error("shell error", "error", err)
	}
	slog.Info("waiting for command to finish...")
	err = cmd.Wait()
	if err != nil {
		slog.Error("shell error", "error", err)
	}
}
