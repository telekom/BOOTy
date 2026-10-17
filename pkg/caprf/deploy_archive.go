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
	"slices"
	"strings"
	"syscall"

	"github.com/telekom/BOOTy/pkg/config"
)

// A deploy archive is a command payload that carries the whole per-job
// deploy tree, like the deploy.cpio of a CAPRF ISO: a gzip-compressed tar
// with deploy/vars and the directories deploy/machine-files,
// deploy/machine-commands and deploy/file-system. A standby agent that was
// booted for one job (a deprovision) needs them for the next job (the
// provision of another machine): the cloud-init seed and the machine files
// are files, not vars.
//
// BOOTy runs as root, so the archive is untrusted input:
//   - only directories and regular files; symlinks, hard links and every
//     other type fail the archive,
//   - every entry is below deploy/, without "..",
//   - files are created with O_EXCL|O_NOFOLLOW in a fresh staging directory,
//     with normalized modes (0644, or 0755 with an execute bit) and the
//     owner of BOOTy,
//   - at most maxArchiveEntries entries and created directories, paths of
//     at most 512 bytes and 16 levels, and maxDeployArchive bytes,
//   - deploy/vars is parsed and validated before /deploy changes.

// deployRoot is the deploy directory of the ramdisk; tests change it.
var deployRoot = "/deploy"

// deployArchiveDirs are the per-job directories that an archive replaces.
var deployArchiveDirs = []string{"machine-files", "machine-commands", "file-system"}

// Limits of a deploy archive; tests lower them.
var (
	maxDeployArchive  int64 = 64 << 20
	maxArchiveEntries       = 10000
)

func isDeployArchive(payload []byte) bool {
	return len(payload) > 2 && payload[0] == 0x1f && payload[1] == 0x8b
}

// errArchiveVars is the error of archive vars that do not parse or validate.
// It names no value: values can be secrets, and the error goes into the
// command ACK and the log.
var errArchiveVars = errors.New("deploy archive vars do not parse or validate")

// deployArchiveConfig unpacks a deploy archive, validates its vars, then
// replaces the per-job directories of /deploy and returns the config of
// the vars. The settings that the running ramdisk uses (transport, agent,
// network, MODE, DRY_RUN, CAPRF_LEGACY_COMPAT and the bearer-token URLs)
// keep the values of the base vars: the agent keeps its token and network.
func deployArchiveConfig(base, payload []byte) (cfg *config.MachineConfig, keys []string, err error) {
	ref, err := ParseVars(bytes.NewReader(base))
	if err != nil {
		return nil, nil, fmt.Errorf("parse base vars: %w", err)
	}
	staging, err := os.MkdirTemp(filepath.Dir(deployRoot), ".booty-archive-")
	if err != nil {
		return nil, nil, fmt.Errorf("deploy archive staging: %w", err)
	}
	defer os.RemoveAll(staging) //nolint:errcheck // best-effort cleanup of the staging tree

	tree := filepath.Join(staging, "deploy")
	if err := unpackDeployArchive(payload, tree); err != nil {
		return nil, nil, err
	}
	cfg, keys, err = archiveVarsConfig(filepath.Join(tree, "vars"))
	if err != nil {
		return nil, nil, err
	}
	if err := swapDeployDirs(tree, deployRoot, filepath.Join(staging, "old")); err != nil {
		return nil, nil, err
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

// archiveVarsConfig reads and validates deploy/vars of the staging tree.
// Like a vars payload, an unknown key fails it; errors name keys only.
func archiveVarsConfig(varsPath string) (cfg *config.MachineConfig, keys []string, err error) {
	vars, err := os.ReadFile(varsPath) //nolint:gosec // path in our staging directory
	if err != nil {
		return nil, nil, errors.New("deploy archive has no deploy/vars")
	}
	keys, err = payloadKeys(vars)
	if err != nil {
		return nil, nil, errArchiveVars
	}
	var unknown []string
	for _, k := range keys {
		if !knownVarKey(k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return nil, nil, fmt.Errorf("deploy archive vars have unknown keys %s", strings.Join(unknown, ","))
	}
	cfg, err = ParseVars(bytes.NewReader(vars))
	if err != nil {
		return nil, nil, errArchiveVars
	}
	return cfg, keys, nil
}

// renameFunc is os.Rename; tests make it fail.
var renameFunc = os.Rename

// swapDeployDirs moves the per-job directories of root to old and the
// ones of tree into root. A failure moves the old ones back.
func swapDeployDirs(tree, root, old string) error {
	if err := os.Mkdir(old, 0o700); err != nil {
		return fmt.Errorf("deploy archive: %w", err)
	}
	var moved []string
	restore := func() {
		for _, dir := range moved {
			_ = os.RemoveAll(filepath.Join(root, dir))
			_ = renameFunc(filepath.Join(old, dir), filepath.Join(root, dir))
		}
	}
	for _, dir := range deployArchiveDirs {
		if err := renameFunc(filepath.Join(root, dir), filepath.Join(old, dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
			restore()
			return fmt.Errorf("replace %s: %w", dir, err)
		}
		moved = append(moved, dir)
	}
	for _, dir := range deployArchiveDirs {
		if err := renameFunc(filepath.Join(tree, dir), filepath.Join(root, dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
			restore()
			return fmt.Errorf("replace %s: %w", dir, err)
		}
	}
	return nil
}

// unpackDeployArchive writes the deploy/ entries of a tar.gz into dst, a
// directory that must not exist yet.
func unpackDeployArchive(payload []byte, dst string) error {
	gz, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("deploy archive: %w", err)
	}
	if err := os.Mkdir(dst, 0o755); err != nil {
		return fmt.Errorf("deploy archive: %w", err)
	}
	u := &unpacker{dst: dst, budget: maxDeployArchive}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("deploy archive: %w", err)
		}
		if err := u.entry(tr, hdr); err != nil {
			return err
		}
	}
}

// unpacker writes the entries of one archive. nodes counts the entries and
// every directory it creates, also the parents that an entry implies, so a
// few long paths can not create many directories.
type unpacker struct {
	dst    string
	budget int64
	nodes  int
}

func (u *unpacker) entry(tr *tar.Reader, hdr *tar.Header) error {
	if err := u.count(); err != nil {
		return err
	}
	rel, err := archivePath(hdr.Name)
	if err != nil || rel == "" {
		return err
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		err = u.mkdirs(rel)
	case tar.TypeReg:
		if slices.Contains(deployArchiveDirs, rel) {
			return fmt.Errorf("deploy archive entry %q must be a directory", hdr.Name)
		}
		if err = u.mkdirs(path.Dir(rel)); err == nil {
			u.budget, err = writeArchiveFile(tr, filepath.Join(u.dst, filepath.FromSlash(rel)), hdr.Mode, u.budget)
		}
	default:
		// Symlinks and hard links would let a later entry write through
		// them, outside the staging directory.
		return fmt.Errorf("deploy archive entry %q has the unsupported type %q: only directories and regular files", hdr.Name, hdr.Typeflag)
	}
	if err != nil {
		return fmt.Errorf("deploy archive entry %q: %w", hdr.Name, err)
	}
	return nil
}

func (u *unpacker) count() error {
	u.nodes++
	if u.nodes > maxArchiveEntries {
		return fmt.Errorf("deploy archive has more than %d entries and directories", maxArchiveEntries)
	}
	return nil
}

// mkdirs creates the directories of rel (slash separated, below dst) one by
// one; each new one counts as a node. An existing non-directory fails.
func (u *unpacker) mkdirs(rel string) error {
	if rel == "." || rel == "" {
		return nil
	}
	cur := u.dst
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		switch {
		case err == nil && fi.IsDir():
			continue
		case err == nil:
			return fmt.Errorf("%s is not a directory", part)
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("check directory: %w", err)
		}
		if err := u.count(); err != nil {
			return err
		}
		if err := os.Mkdir(cur, 0o755); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
	}
	return nil
}

// Path limits of an archive entry.
const (
	maxArchivePathLen   = 512
	maxArchivePathDepth = 16
)

// archivePath returns the path of an entry below deploy/, or "" for the
// deploy directory itself. Paths outside deploy/, with ".." or absolute
// paths fail.
func archivePath(name string) (string, error) {
	if len(name) > maxArchivePathLen {
		return "", fmt.Errorf("deploy archive entry path is longer than %d bytes", maxArchivePathLen)
	}
	trimmed := strings.TrimPrefix(name, "./")
	if strings.Count(strings.TrimSuffix(trimmed, "/"), "/") >= maxArchivePathDepth {
		return "", fmt.Errorf("deploy archive entry %q is deeper than %d levels", name, maxArchivePathDepth)
	}
	for _, part := range strings.Split(strings.TrimSuffix(trimmed, "/"), "/") {
		if part == ".." {
			return "", fmt.Errorf("deploy archive entry %q has a parent directory element", name)
		}
	}
	clean := path.Clean(trimmed)
	if clean == "deploy" {
		return "", nil
	}
	rel, ok := strings.CutPrefix(clean, "deploy/")
	if !ok || path.IsAbs(trimmed) || rel == "" || rel == "." {
		return "", fmt.Errorf("deploy archive entry %q is outside deploy/", name)
	}
	return rel, nil
}

// writeArchiveFile creates a new file: O_EXCL fails on any existing name,
// O_NOFOLLOW on a symlink. The mode is 0644, or 0755 with an execute bit.
// It returns the byte budget that is left.
func writeArchiveFile(r io.Reader, target string, mode, budget int64) (int64, error) {
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, perm) //nolint:gosec // path checked by archivePath
	if err != nil {
		return budget, fmt.Errorf("create file: %w", err)
	}
	n, err := io.Copy(f, io.LimitReader(r, budget+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return budget, err
	}
	if n > budget {
		return 0, fmt.Errorf("deploy archive is larger than %d bytes", maxDeployArchive)
	}
	// The mode is set explicitly: OpenFile applies the umask.
	if err := os.Chmod(target, perm); err != nil {
		return budget, fmt.Errorf("set mode: %w", err)
	}
	return budget - n, nil
}
