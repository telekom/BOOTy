//go:build linux

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/telekom/BOOTy/pkg/espsync"
	"golang.org/x/sys/unix"
)

func main() {
	source := flag.String("source", "/boot/efi", "mounted source ESP")
	firstPartUUID := flag.String("partuuid-a", "", "first mirrored ESP PARTUUID")
	secondPartUUID := flag.String("partuuid-b", "", "second mirrored ESP PARTUUID")
	sourcePartUUID := flag.String("source-partuuid", "", "legacy fixed source ESP PARTUUID")
	destination := flag.String("destination", "", "destination ESP block device or mounted directory")
	mountpoint := flag.String("mountpoint", "/run/booty/esp-sync", "private destination mountpoint")
	lockPath := flag.String("lock", "/run/lock/booty-esp-sync.lock", "synchronization lock")
	marker := flag.String("marker", "/run/booty/esp-sync.ready", "optional marker written after a successful synchronization")
	once := flag.Bool("once", false, "synchronize once and exit")
	flag.Parse()
	if (*firstPartUUID == "") != (*secondPartUUID == "") {
		fatal(errors.New("partuuid-a and partuuid-b must be provided together"))
	}
	if *destination == "" && *firstPartUUID == "" {
		fatal(errors.New("destination is required"))
	}
	if *once {
		runOnce(*source, *sourcePartUUID, *firstPartUUID, *secondPartUUID, *destination, *mountpoint, *lockPath, *marker)
		return
	}
	if err := runService(*source, *sourcePartUUID, *firstPartUUID, *secondPartUUID, *destination, *mountpoint, *lockPath, *marker); err != nil {
		fatal(err)
	}
}

func runOnce(source, sourcePartUUID, firstPartUUID, secondPartUUID, destination, mountpoint, lockPath, marker string) {
	if firstPartUUID != "" {
		if err := syncPair(source, firstPartUUID, secondPartUUID, mountpoint, lockPath, marker); err != nil {
			fatal(err)
		}
		return
	}
	mounted, err := mountDestination(destination, mountpoint)
	if err != nil {
		fatal(err)
	}
	if mounted {
		defer unmount(mountpoint)
	}
	if sourcePartUUID != "" {
		if err := espsync.ValidateMountedSource(source, sourcePartUUID); err != nil {
			fatal(err)
		}
	}
	if err := espsync.RunOnce(source, mountpointOrDestination(mounted, mountpoint, destination), lockPath); err != nil {
		fatal(err)
	}
	if err := espsync.WriteMarker(marker); err != nil {
		fatal(err)
	}
}

func runService(source, sourcePartUUID, firstPartUUID, secondPartUUID, destination, mountpoint, lockPath, marker string) error {
	if firstPartUUID != "" {
		if err := syncPair(source, firstPartUUID, secondPartUUID, mountpoint, lockPath, marker); err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		return espsync.WatchFuncWithMarker(source, func() error {
			return syncPair(source, firstPartUUID, secondPartUUID, mountpoint, lockPath, marker)
		}, marker, ctx.Done())
	}
	mounted, err := mountDestination(destination, mountpoint)
	if err != nil {
		return err
	}
	if mounted {
		defer unmount(mountpoint)
	}
	target := mountpointOrDestination(mounted, mountpoint, destination)
	if sourcePartUUID != "" {
		if err := espsync.ValidateMountedSource(source, sourcePartUUID); err != nil {
			return err
		}
	}
	if err := espsync.RunOnce(source, target, lockPath); err != nil {
		return err
	}
	if err := espsync.WriteMarker(marker); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return espsync.WatchWithMarker(source, target, lockPath, marker, ctx.Done())
}

func syncPair(source, firstPartUUID, secondPartUUID, mountpoint, lockPath, marker string) error {
	destination, err := espsync.PeerDevice(source, firstPartUUID, secondPartUUID)
	if err != nil {
		return err
	}
	mounted, err := mountDestination(destination, mountpoint)
	if err != nil {
		return err
	}
	if mounted {
		defer unmount(mountpoint)
	}
	if err := espsync.RunOnce(source, mountpointOrDestination(mounted, mountpoint, destination), lockPath); err != nil {
		return err
	}
	return espsync.WriteMarker(marker)
}

func mountDestination(device, mountpoint string) (bool, error) {
	info, err := os.Stat(device)
	if err == nil && info.IsDir() {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if !filepath.IsAbs(mountpoint) {
		return false, fmt.Errorf("mountpoint must be absolute")
	}
	if err := os.MkdirAll(mountpoint, 0o700); err != nil {
		return false, err
	}
	if err := unix.Mount(device, mountpoint, "vfat", unix.MS_NODEV|unix.MS_NOSUID|unix.MS_NOEXEC, "umask=0077"); err != nil {
		return false, fmt.Errorf("mount destination ESP: %w", err)
	}
	return true, nil
}

func unmount(path string) {
	if err := unix.Unmount(path, 0); err != nil {
		slog.Warn("unmount destination ESP", "path", path, "error", err)
	}
}
func mountpointOrDestination(mounted bool, mountpoint, destination string) string {
	if mounted {
		return mountpoint
	}
	return destination
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
