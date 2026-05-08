//go:build linux

package crash

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
)

const defaultMountPoint = "/run/booty/crash-root"

// InspectOptions controls startup inspection.
type InspectOptions struct {
	MountPoint string
	OutputDir  string
	PstorePath string
	Log        *slog.Logger
}

// ShouldInspect reports whether crash inspection should run for cfg.
func ShouldInspect(cfg *config.MachineConfig) (bool, string) {
	if cfg == nil || !cfg.CrashArtifactsEnabled {
		return false, "disabled"
	}
	mode := strings.TrimSpace(cfg.Mode)
	if mode == "" {
		mode = "provision"
	}
	switch mode {
	case "provision", "deprovision", "soft-deprovision", "soft":
		return true, ""
	case "dry-run":
		return false, "dry-run"
	case "standby":
		return false, "standby"
	default:
		return false, "unsupported-mode"
	}
}

// InspectStartup scans the existing target OS for crash artifacts and uploads them.
func InspectStartup(ctx context.Context, cfg *config.MachineConfig, diskMgr *disk.Manager, uploader Uploader, opts InspectOptions) (*InspectResult, error) {
	log := opts.Log
	if log == nil {
		log = slog.Default().With("component", "crash")
	}
	if ok, reason := ShouldInspect(cfg); !ok {
		return &InspectResult{SkipReason: reason}, nil
	}
	if diskMgr == nil {
		return &InspectResult{Ran: true, SkipReason: "missing-disk-manager"}, nil
	}
	if uploader == nil {
		return &InspectResult{Ran: true, SkipReason: "missing-uploader"}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("crash inspection canceled: %w", err)
	}

	targetDisk, err := resolveTargetDisk(ctx, cfg, diskMgr)
	if err != nil {
		log.Warn("crash artifact disk detection failed", "error", err)
		return &InspectResult{Ran: true, SkipReason: "disk-detection-failed"}, nil
	}

	parts, err := diskMgr.ParsePartitions(ctx, targetDisk)
	if err != nil {
		log.Warn("crash artifact partition parsing failed", "disk", targetDisk, "error", err)
		return &InspectResult{Ran: true, SkipReason: "partition-parse-failed"}, nil
	}
	root, err := diskMgr.FindRootPartition(parts)
	if err != nil {
		reason := unsupportedPartitionReason(parts)
		log.Warn("crash artifact root partition not found", "disk", targetDisk, "reason", reason, "error", err)
		return &InspectResult{Ran: true, SkipReason: reason}, nil
	}

	mountPoint := opts.MountPoint
	if mountPoint == "" {
		mountPoint = defaultMountPoint
	}
	if err := os.MkdirAll(filepath.Dir(mountPoint), 0o755); err != nil {
		return &InspectResult{Ran: true, SkipReason: "mountpoint-create-failed"}, nil
	}
	if err := diskMgr.MountPartitionReadOnly(ctx, root.Node, mountPoint); err != nil {
		log.Warn("crash artifact read-only mount failed", "partition", root.Node, "error", err)
		return &InspectResult{Ran: true, SkipReason: "mount-failed"}, nil
	}
	defer func() {
		if err := diskMgr.Unmount(mountPoint); err != nil {
			log.Warn("crash artifact unmount failed", "mountpoint", mountPoint, "error", err)
		}
	}()

	collectResult, err := Collect(ctx, CollectOptions{
		RootPath:      mountPoint,
		PstorePath:    opts.PstorePath,
		OutputDir:     opts.OutputDir,
		TargetDisk:    targetDisk,
		RootPartition: root.Node,
		MountPoint:    mountPoint,
		MaxBytes:      int64(cfg.CrashArtifactsMaxMB) * 1024 * 1024,
		Config:        cfg,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		log.Warn("crash artifact collection failed", "error", err)
		return &InspectResult{Ran: true, SkipReason: "collection-failed"}, nil
	}
	result := &InspectResult{Ran: true, EvidenceFound: collectResult.EvidenceFound, ArchivePath: collectResult.ArchivePath, Manifest: &collectResult.Manifest}
	if !collectResult.EvidenceFound {
		result.SkipReason = "no-evidence"
		return result, nil
	}

	req := &PrepareRequest{
		Manifest:      collectResult.Manifest,
		ArchiveBytes:  collectResult.Manifest.Scan.ArchiveBytes,
		ArtifactCount: len(collectResult.Manifest.Artifacts),
		TotalBytes:    collectResult.Manifest.Scan.TotalBytes,
	}
	if err := uploader.ReportCrashArtifacts(ctx, req, collectResult.ArchivePath); err != nil {
		log.Warn("crash artifact upload failed", "error", err)
		result.UploadError = err
		return result, nil
	}
	result.Uploaded = true
	return result, nil
}

func resolveTargetDisk(ctx context.Context, cfg *config.MachineConfig, diskMgr *disk.Manager) (string, error) {
	if cfg != nil && cfg.PartitionLayout != nil {
		if device := strings.TrimSpace(cfg.PartitionLayout.Device); device != "" {
			return device, nil
		}
	}
	if cfg != nil && strings.TrimSpace(cfg.DiskDevice) != "" {
		return strings.TrimSpace(cfg.DiskDevice), nil
	}
	minSize := 0
	if cfg != nil {
		minSize = cfg.MinDiskSizeGB
	}
	return diskMgr.DetectDisk(ctx, minSize)
}

func unsupportedPartitionReason(parts []disk.Partition) string {
	for _, part := range parts {
		if strings.EqualFold(part.Type, disk.LinuxLVMGUID) {
			return "lvm_root_unsupported"
		}
	}
	return "root-partition-not-found"
}
