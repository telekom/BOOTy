//go:build linux

package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/efi"
	"github.com/telekom/BOOTy/pkg/oob"
	"github.com/telekom/BOOTy/pkg/provision"
)

const consoleActivePath = "/sys/class/tty/console/active"

// startOOBStatus installs the out-of-band status reporter (OOB_STATUS,
// OOB_EFI_VAR). With OOB_EFI_VAR it logs the result of the previous run.
func startOOBStatus(ctx context.Context, cfg *config.MachineConfig) {
	opts := oob.Options{
		Serial:   cfg.Telemetry.OOBStatus != config.OOBStatusOff,
		Consoles: oob.ActiveConsoles(consoleActivePath),
		Mode:     cfg.Mode,
		Version:  Version,
	}
	if config.BoolOr(cfg.Telemetry.OOBEFIVar, false) {
		opts.Vars = efiVarsForOOB(ctx)
	}
	r := oob.NewReporter(opts)
	slog.Info("out-of-band status", "run", r.Run(), "serial", opts.Serial, "efi_var", opts.Vars != nil)
	oob.Install(r)
}

func efiVarsForOOB(ctx context.Context) *efi.EFIVarReader {
	if err := provision.NewConfigurator(nil).MountEFIVars(ctx); err != nil {
		slog.Warn("no BootyLastResult UEFI variable: efivarfs is not available", "error", err)
		return nil
	}
	if _, err := os.Stat("/sys/firmware/efi/efivars"); err != nil {
		slog.Info("no BootyLastResult UEFI variable: not a UEFI host")
		return nil
	}
	vars := efi.NewEFIVarReader("")
	oob.LogLastResult(vars)
	return vars
}

// networkFailed reports a failed network or token setup out of band and
// returns its failure code.
func networkFailed(err error) string {
	code := provision.CodeOf(err)
	oob.Fail("network-setup", code)
	return code
}
