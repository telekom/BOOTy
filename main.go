//go:build linux

package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/telekom/BOOTy/pkg/caprf"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/image"
	"github.com/telekom/BOOTy/pkg/plunderclient"
	"github.com/telekom/BOOTy/pkg/plunderclient/types"
	"github.com/telekom/BOOTy/pkg/utils"

	"github.com/telekom/BOOTy/pkg/realm"
	"github.com/telekom/BOOTy/pkg/ux"
)

const varsPath = "/deploy/vars"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	setupMountsAndDevices()

	slog.Info("Starting DHCP client")
	go func() {
		if err := realm.DHCPClient(); err != nil {
			slog.Error("DHCP client error", "error", err)
		}
	}()

	slog.Info("Starting BOOTy")
	time.Sleep(time.Second * 2)
	ux.Captain()
	ux.SysInfo()

	slog.Info("Beginning provisioning process")
	ctx := context.Background()

	// Detect mode: CAPRF (ISO with /deploy/vars) or legacy (BOOTYURL env).
	if _, err := os.Stat(varsPath); err == nil {
		runCAPRF(ctx)
	} else {
		runLegacy()
	}
}

// setupMountsAndDevices performs early init: mount filesystems and create devices.
func setupMountsAndDevices() {
	m := realm.DefaultMounts()
	d := realm.DefaultDevices()

	for _, name := range []string{"dev", "proc", "tmp", "sys"} {
		mt := m.GetMount(name)
		mt.CreateMount = true
		mt.EnableMount = true
	}

	if err := m.CreateFolder(); err != nil {
		slog.Error("Failed to create folders", "error", err)
	}
	if err := m.MountNamed("dev", true); err != nil {
		slog.Error("Failed to mount dev", "error", err)
	}
	if err := d.CreateDevice(); err != nil {
		slog.Error("Failed to create devices", "error", err)
	}
	if err := m.MountAll(); err != nil {
		slog.Error("Failed to mount filesystems", "error", err)
	}
}

// runCAPRF runs the CAPRF provisioning flow (ISO-based, /deploy/vars config).
func runCAPRF(ctx context.Context) {
	client, err := caprf.New(varsPath)
	if err != nil {
		slog.Error("Failed to create CAPRF client", "error", err)
		realm.Reboot()
	}

	cfg, err := client.GetConfig(ctx)
	if err != nil {
		slog.Error("Failed to get CAPRF config", "error", err)
		realm.Reboot()
	}

	// Wire remote log shipping.
	if cfg.LogURL != "" {
		remote := caprf.NewRemoteHandler(client, slog.Default().Handler(), slog.LevelInfo, 256)
		defer remote.Close()
		slog.SetDefault(slog.New(remote))
	}

	slog.Info("CAPRF mode active",
		"hostname", cfg.Hostname,
		"mode", cfg.Mode,
		"images", cfg.ImageURLs,
	)

	if err := client.ReportStatus(ctx, config.StatusInit, "provisioning started"); err != nil {
		slog.Error("Failed to report init status", "error", err)
	}

	// Write images to disk.
	for _, imgURL := range cfg.ImageURLs {
		slog.Info("Writing image", "url", imgURL)
		if err := image.Write(imgURL, "/dev/sda", false); err != nil {
			slog.Error("Image write failed", "error", err)
			_ = client.ReportStatus(ctx, config.StatusError, err.Error())
			realm.Reboot()
		}
	}

	slog.Info("Image written, beginning disk management")

	// PartProbe + LVM + grow (when configured).
	if err := realm.PartProbe("/dev/sda"); err != nil {
		slog.Error("PartProbe failed", "error", err)
		_ = client.ReportStatus(ctx, config.StatusError, err.Error())
		realm.Reboot()
	}

	if err := realm.EnableLVM(); err != nil {
		slog.Error("LVM enable failed", "error", err)
		_ = client.ReportStatus(ctx, config.StatusError, err.Error())
		realm.Reboot()
	}

	if err := client.ReportStatus(ctx, config.StatusSuccess, "provisioning complete"); err != nil {
		slog.Error("Failed to report success status", "error", err)
	}

	slog.Info("BOOTy CAPRF provisioning complete, rebooting")
	time.Sleep(time.Second * 2)
	realm.Reboot()
}

// runLegacy runs the original BOOTy flow using BOOTYURL environment variable.
func runLegacy() {
	mac, err := realm.GetMAC()
	if err != nil {
		slog.Error("Failed to get MAC address", "error", err)
		realm.Shell()
	}

	cfg, err := plunderclient.GetConfigForAddress(utils.DashMac(mac))
	if err != nil {
		slog.Error("Error with remote server", "error", err)
		slog.Info("Rebooting in 10 seconds")
		time.Sleep(time.Second * 10)
		realm.Reboot()
	}

	switch cfg.Action {
	case types.ReadImage:
		err = image.Read(cfg.SourceDevice, cfg.DestinationAddress, mac, cfg.Compressed)
		if err != nil {
			slog.Error("Read Image Error", "error", err)
			onError(cfg)
		}
		slog.Info("Image written successfully, restarting in 5 seconds")
		time.Sleep(time.Second * 5)
		realm.Reboot()

	case types.WriteImage:
		err = image.Write(cfg.SourceImage, cfg.DestinationDevice, cfg.Compressed)
		if err != nil {
			slog.Error("Write Image Error", "error", err)
			onError(cfg)
		}

	default:
		slog.Error("Unknown action passed to deployment image, restarting in 10 seconds", "action", cfg.Action)
		time.Sleep(time.Second * 10)
		realm.Reboot()
	}

	slog.Info("Beginning Disk Management")

	err = realm.PartProbe(cfg.DestinationDevice)
	if err != nil {
		slog.Error("Disk Error", "error", err)
		onError(cfg)
	}

	err = realm.EnableLVM()
	if err != nil {
		slog.Error("Disk Error", "error", err)
		onError(cfg)
	}

	rv, err := realm.MountRootVolume(cfg.LVMRootName)
	if err != nil {
		slog.Error("Disk Error", "error", err)
		onError(cfg)
	}

	err = realm.GrowLVMRoot(cfg.DestinationDevice, cfg.LVMRootName, cfg.GrowPartition)
	if err != nil {
		slog.Error("Disk Error", "error", err)
		onError(cfg)
	}

	slog.Info("Starting Networking configuration")
	err = realm.WriteNetPlan("/mnt", cfg)
	if err != nil {
		slog.Error("Network Error", "error", err)
		onError(cfg)
	}

	slog.Info("Applying Networking configuration")
	err = realm.ApplyNetplan("/mnt")
	if err != nil {
		slog.Error("Network Error", "error", err)
		onError(cfg)
	}

	slog.Info("Un Mounting boot volume")
	err = rv.UnMountNamed("dev")
	if err != nil {
		slog.Error("UnMounting Error", "error", err)
		onError(cfg)
	}
	err = rv.UnMountNamed("proc")
	if err != nil {
		slog.Error("UnMounting Error", "error", err)
		onError(cfg)
	}
	err = rv.UnMountAll()
	if err != nil {
		slog.Error("UnMounting Error", "error", err)
		onError(cfg)
	}

	if cfg.DropToShell {
		realm.Shell()
	}

	slog.Info("BOOTy is now exiting, system will reboot")
	time.Sleep(time.Second * 2)
	realm.Reboot()
}

// onError handles error recovery in legacy mode.
func onError(cfg *types.BootyConfig) {
	if cfg.WipeDevice {
		if err := realm.Wipe(cfg.DestinationDevice); err != nil {
			slog.Error("Wipe error", "error", err)
		}
	}

	if cfg.DropToShell {
		realm.Shell()
	}
	time.Sleep(time.Second * 2)
	realm.Reboot()
}
