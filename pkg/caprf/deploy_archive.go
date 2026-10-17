package caprf

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/telekom/BOOTy/pkg/config"
)

// A deploy archive is a command payload that carries the whole per-job
// deploy tree, like the deploy.cpio of a CAPRF ISO: a gzip-compressed tar
// with deploy/vars and the directories deploy/machine-files,
// deploy/machine-commands and deploy/file-system. A standby agent that was
// booted for one job (a deprovision) needs them for the next job (the
// provision of another machine): the cloud-init seed and the machine files
// are files, not vars.

// deployRoot is the deploy directory of the ramdisk; tests change it.
var deployRoot = "/deploy"

// deployArchiveDirs are the per-job directories that an archive replaces.
var deployArchiveDirs = []string{"machine-files", "machine-commands", "file-system"}

// maxDeployArchive bounds the unpacked size of an archive in RAM.
const maxDeployArchive = 64 << 20

func isDeployArchive(payload []byte) bool {
	return len(payload) > 2 && payload[0] == 0x1f && payload[1] == 0x8b
}

// deployArchiveConfig applies a deploy archive and returns the config of
// its vars. The settings that the running ramdisk uses (transport, agent,
// network, MODE, DRY_RUN, CAPRF_LEGACY_COMPAT and the bearer-token URLs)
// keep the values of the base vars: the agent keeps its token and network.
func deployArchiveConfig(base, payload []byte) (cfg *config.MachineConfig, keys []string, err error) {
	ref, err := ParseVars(bytes.NewReader(base))
	if err != nil {
		return nil, nil, fmt.Errorf("parse base vars: %w", err)
	}
	vars, err := applyDeployArchive(payload, deployRoot)
	if err != nil {
		return nil, nil, err
	}
	keys, err = payloadKeys(vars)
	if err != nil {
		return nil, nil, fmt.Errorf("deploy archive vars: %w", err)
	}
	cfg, err = ParseVars(bytes.NewReader(vars))
	if err != nil {
		return nil, nil, fmt.Errorf("deploy archive vars do not parse or validate: %w", err)
	}
	cfg.Transport, cfg.Agent, cfg.Network = ref.Transport, ref.Agent, ref.Network
	cfg.Mode, cfg.DryRun, cfg.LegacyCompat = ref.Mode, ref.DryRun, ref.LegacyCompat
	cfg.Provision.Inventory.URL = ref.Provision.Inventory.URL
	cfg.Provision.Firmware.URL = ref.Provision.Firmware.URL
	cfg.Health.ReportURL = ref.Health.ReportURL
	cfg.Telemetry.URL, cfg.Telemetry.MetricsURL, cfg.Telemetry.EventURL = ref.Telemetry.URL, ref.Telemetry.MetricsURL, ref.Telemetry.EventURL
	cfg.Provision.CrashArtifacts.PrepareURL = ref.Provision.CrashArtifacts.PrepareURL
	cfg.Provision.CrashArtifacts.UploadURL = ref.Provision.CrashArtifacts.UploadURL
	return cfg, keys, nil
}

// applyDeployArchive unpacks the archive next to root, replaces the
// per-job directories of root with the unpacked ones (a directory that the
// archive does not have is removed) and returns deploy/vars.
func applyDeployArchive(payload []byte, root string) ([]byte, error) {
	staging, err := os.MkdirTemp(root, ".archive-")
	if err != nil {
		return nil, fmt.Errorf("deploy archive staging: %w", err)
	}
	defer os.RemoveAll(staging) //nolint:errcheck // best-effort cleanup of the staging tree
	if err := unpackDeployArchive(payload, staging); err != nil {
		return nil, err
	}
	vars, err := os.ReadFile(filepath.Join(staging, "vars"))
	if err != nil {
		return nil, fmt.Errorf("deploy archive has no deploy/vars: %w", err)
	}
	for _, dir := range deployArchiveDirs {
		dst := filepath.Join(root, dir)
		if err := os.RemoveAll(dst); err != nil {
			return nil, fmt.Errorf("replace %s: %w", dst, err)
		}
		if err := os.Rename(filepath.Join(staging, dir), dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("replace %s: %w", dst, err)
		}
	}
	return vars, nil
}

// unpackDeployArchive writes the deploy/ entries of a tar.gz below dst.
// Only directories, regular files and symlinks are allowed; no entry may
// leave dst or pass through a symlink of the archive.
func unpackDeployArchive(payload []byte, dst string) error {
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("deploy archive: %w", err)
	}
	tr := tar.NewReader(gz)
	links := map[string]bool{}
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("deploy archive: %w", err)
		}
		rel, err := archivePath(hdr.Name, links)
		if err != nil || rel == "" {
			if err != nil {
				return err
			}
			continue
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		total += hdr.Size
		if total > maxDeployArchive {
			return fmt.Errorf("deploy archive is larger than %d bytes", maxDeployArchive)
		}
		if err := writeArchiveEntry(tr, hdr, target); err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeSymlink {
			links[rel] = true
		}
	}
}

// archivePath returns the path of an entry below deploy/, or "" for the
// deploy directory itself. It rejects paths outside deploy/ and paths
// below a symlink of the archive.
func archivePath(name string, links map[string]bool) (string, error) {
	clean := path.Clean(strings.TrimPrefix(name, "./"))
	rel, ok := strings.CutPrefix(clean, "deploy/")
	switch {
	case clean == "deploy":
		return "", nil
	case !ok || rel == "" || strings.HasPrefix(rel, "../") || rel == ".." || path.IsAbs(clean):
		return "", fmt.Errorf("deploy archive entry %q is outside deploy/", name)
	}
	for p := path.Dir(rel); p != "."; p = path.Dir(p) {
		if links[p] {
			return "", fmt.Errorf("deploy archive entry %q is below the symlink %s", name, p)
		}
	}
	return rel, nil
}

func writeArchiveEntry(tr *tar.Reader, hdr *tar.Header, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("deploy archive %s: %w", hdr.Name, err)
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("deploy archive %s: %w", hdr.Name, err)
		}
	case tar.TypeReg:
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode&0o777)) //nolint:gosec // path checked by archivePath
		if err != nil {
			return fmt.Errorf("deploy archive %s: %w", hdr.Name, err)
		}
		_, err = io.CopyN(f, tr, hdr.Size)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("deploy archive %s: %w", hdr.Name, err)
		}
	case tar.TypeSymlink:
		// Machine files keep their symlinks; the link target is not followed.
		if err := os.Symlink(hdr.Linkname, target); err != nil {
			return fmt.Errorf("deploy archive %s: %w", hdr.Name, err)
		}
	default:
		return fmt.Errorf("deploy archive entry %q has the unsupported type %q", hdr.Name, hdr.Typeflag)
	}
	return nil
}
