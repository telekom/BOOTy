//go:build linux

package crash

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/telekom/BOOTy/pkg/buildinfo"
	"github.com/telekom/BOOTy/pkg/config"
	debugdump "github.com/telekom/BOOTy/pkg/debug"
	"github.com/telekom/BOOTy/pkg/firmware"
	"github.com/telekom/BOOTy/pkg/inventory"
)

const (
	manifestVersion = 1
	defaultPstore   = "/sys/fs/pstore"
)

var textEvidenceMarkers = []string{
	"kernel panic",
	"panic:",
	"oops:",
	"kernel oops",
	"bug:",
	"call trace",
	"soft lockup",
	"hard lockup",
	"watchdog",
	"hung task",
	"ramoops",
	"vmcore",
}

var targetRootPatterns = []string{
	"var/crash",
	"var/lib/systemd/coredump",
	"var/log/journal",
	"var/log/kern.log*",
	"var/log/messages*",
	"var/log/syslog*",
	"var/log/dmesg*",
	"var/log/kdump*",
}

// CollectOptions controls crash artifact collection.
type CollectOptions struct {
	RootPath      string
	PstorePath    string
	OutputDir     string
	TargetDisk    string
	RootPartition string
	MountPoint    string
	MaxBytes      int64
	Config        *config.MachineConfig
}

type candidate struct {
	sourcePath  string
	archivePath string
	kind        string
	size        int64
	evidence    []string
}

// Collect gathers crash artifacts and metadata into a tar.gz archive.
func Collect(ctx context.Context, opts *CollectOptions) (*CollectResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("collect crash artifacts canceled: %w", err)
	}
	if opts == nil {
		opts = &CollectOptions{}
	}

	if opts.PstorePath == "" {
		opts.PstorePath = defaultPstore
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}

	metadata := collectMetadata(ctx, opts.Config)
	candidates, skipped := collectCandidates(ctx, opts)
	evidenceFound := hasEvidence(candidates)

	manifest := Manifest{
		Version:   manifestVersion,
		CreatedAt: time.Now().UTC(),
		Scan: ScanMetadata{
			TargetDisk:    opts.TargetDisk,
			RootPartition: opts.RootPartition,
			MountPoint:    opts.MountPoint,
			PstorePath:    opts.PstorePath,
			EvidenceFound: evidenceFound,
		},
		Metadata: metadata,
		Skipped:  skipped,
	}

	if !evidenceFound {
		manifest.Scan.SkippedCount = len(manifest.Skipped)
		return &CollectResult{Manifest: manifest, EvidenceFound: false}, nil
	}

	selected := selectArtifacts(candidates, opts.MaxBytes, &manifest)
	manifest.Artifacts = selected
	manifest.Scan.ArtifactCount = len(selected)
	manifest.Scan.SkippedCount = len(manifest.Skipped)
	for _, artifact := range selected {
		manifest.Scan.TotalBytes += artifact.SizeBytes
	}

	archivePath, archiveBytes, err := writeArchive(opts.OutputDir, &manifest)
	if err != nil {
		return nil, err
	}
	manifest.Scan.ArchiveBytes = archiveBytes

	return &CollectResult{Manifest: manifest, ArchivePath: archivePath, EvidenceFound: true}, nil
}

func collectCandidates(ctx context.Context, opts *CollectOptions) ([]candidate, []SkippedArtifact) {
	var candidates []candidate
	var skipped []SkippedArtifact
	if opts.RootPath != "" {
		rootCandidates, rootSkipped := scanTargetRoot(ctx, opts.RootPath)
		candidates = append(candidates, rootCandidates...)
		skipped = append(skipped, rootSkipped...)
	}
	pstoreCandidates, pstoreSkipped := scanPstore(ctx, opts.PstorePath)
	candidates = append(candidates, pstoreCandidates...)
	skipped = append(skipped, pstoreSkipped...)
	return candidates, skipped
}

func scanTargetRoot(ctx context.Context, root string) ([]candidate, []SkippedArtifact) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, []SkippedArtifact{{SourcePath: root, Reason: "resolve_root", Error: err.Error()}}
	}

	var candidates []candidate
	var skipped []SkippedArtifact
	for _, pattern := range targetRootPatterns {
		matches, _ := filepath.Glob(filepath.Join(rootAbs, filepath.FromSlash(pattern)))
		for _, match := range matches {
			walked, walkSkipped := walkCandidatePath(ctx, rootAbs, match, "target-root")
			candidates = append(candidates, walked...)
			skipped = append(skipped, walkSkipped...)
		}
	}
	return candidates, skipped
}

func scanPstore(ctx context.Context, pstorePath string) ([]candidate, []SkippedArtifact) {
	if pstorePath == "" {
		return nil, nil
	}
	info, err := os.Stat(pstorePath)
	if err != nil || !info.IsDir() {
		return nil, nil
	}
	return walkCandidatePath(ctx, pstorePath, pstorePath, "pstore")
}

func walkCandidatePath(ctx context.Context, rootAbs, path, archivePrefix string) ([]candidate, []SkippedArtifact) {
	var candidates []candidate
	var skipped []SkippedArtifact
	err := filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("walk candidate path canceled: %w", err)
		}
		if walkErr != nil {
			return skipCandidate(&skipped, current, "walk_error", walkErr)
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			skipped = append(skipped, SkippedArtifact{SourcePath: current, Reason: "symlink_skipped"})
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return skipCandidate(&skipped, current, "stat_error", infoErr)
		}
		if !info.Mode().IsRegular() {
			skipped = append(skipped, SkippedArtifact{SourcePath: current, Reason: "not_regular"})
			return nil
		}
		rel, ok := safeRel(rootAbs, current)
		if !ok {
			skipped = append(skipped, SkippedArtifact{SourcePath: current, Reason: "path_escape"})
			return nil
		}
		kind, evidence := classifyArtifact(current, rel, archivePrefix)
		candidates = append(candidates, candidate{
			sourcePath:  current,
			archivePath: filepath.ToSlash(filepath.Join(archivePrefix, rel)),
			kind:        kind,
			size:        info.Size(),
			evidence:    evidence,
		})
		return nil
	})
	if err != nil {
		skipped = append(skipped, SkippedArtifact{SourcePath: path, Reason: "walk_canceled", Error: err.Error()})
	}
	return candidates, skipped
}

func skipCandidate(skipped *[]SkippedArtifact, sourcePath, reason string, err error) error {
	*skipped = append(*skipped, SkippedArtifact{SourcePath: sourcePath, Reason: reason, Error: err.Error()})
	return nil
}

func safeRel(rootAbs, current string) (string, bool) {
	absCurrent, err := filepath.Abs(current)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootAbs, absCurrent)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

func classifyArtifact(path, rel, archivePrefix string) (kind string, evidence []string) {
	lowerRel := strings.ToLower(filepath.ToSlash(rel))
	lowerBase := strings.ToLower(filepath.Base(path))
	switch {
	case archivePrefix == "pstore":
		return "pstore", []string{"pstore"}
	case strings.Contains(lowerRel, "var/crash") || strings.Contains(lowerBase, "vmcore"):
		return "kdump", []string{"crash-file"}
	case strings.Contains(lowerRel, "systemd/coredump"):
		return "coredump", []string{"coredump-file"}
	case strings.Contains(lowerRel, "journal"):
		return "journal", nil
	default:
		evidence := scanTextEvidence(path)
		return "kernel-log", evidence
	}
}

func scanTextEvidence(path string) []string {
	f, err := os.Open(path) //nolint:gosec // allowlisted local artifact path
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck // best-effort close
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return nil
	}
	lower := strings.ToLower(string(data))
	var evidence []string
	for _, marker := range textEvidenceMarkers {
		if strings.Contains(lower, marker) {
			evidence = append(evidence, marker)
		}
	}
	return evidence
}

func hasEvidence(candidates []candidate) bool {
	for _, c := range candidates {
		if len(c.evidence) > 0 {
			return true
		}
	}
	return false
}

func selectArtifacts(candidates []candidate, maxBytes int64, manifest *Manifest) []Artifact {
	var selected []Artifact
	var used int64
	for _, c := range candidates {
		if c.size <= 0 {
			manifest.Skipped = append(manifest.Skipped, SkippedArtifact{SourcePath: c.sourcePath, Reason: "empty_file"})
			continue
		}
		if used+c.size > maxBytes {
			manifest.Skipped = append(manifest.Skipped, SkippedArtifact{
				SourcePath: c.sourcePath,
				Reason:     "size_limit",
				SizeBytes:  c.size,
			})
			continue
		}
		used += c.size
		selected = append(selected, Artifact{
			SourcePath:  c.sourcePath,
			ArchivePath: c.archivePath,
			Kind:        c.kind,
			SizeBytes:   c.size,
			Evidence:    c.evidence,
		})
	}
	return selected
}

func collectMetadata(ctx context.Context, cfg *config.MachineConfig) HostMetadata {
	metadata := HostMetadata{Machine: machineMetadata(cfg)}
	metadata.Inventory = marshalMetadata("inventory", inventory.Collect, &metadata.Errors)
	metadata.Firmware = marshalMetadata("firmware", firmware.Collect, &metadata.Errors)
	metadata.Debug = marshalValue("debug", debugdump.Collect(ctx), &metadata.Errors)
	metadata.BuildInfo = marshalValue("buildInfo", buildinfo.Get(), &metadata.Errors)
	return metadata
}

func machineMetadata(cfg *config.MachineConfig) MachineMetadata {
	meta := MachineMetadata{CollectedAt: time.Now().UTC()}
	if cfg == nil {
		if hostname, err := os.Hostname(); err == nil {
			meta.Hostname = hostname
		}
		return meta
	}
	meta.Hostname = cfg.Hostname
	meta.ProviderID = cfg.ProviderID
	meta.Mode = cfg.Mode
	meta.Region = cfg.Region
	meta.FailureDomain = cfg.FailureDomain
	meta.ImageMode = cfg.ImageMode
	meta.NetworkMode = cfg.NetworkMode
	meta.BGPPeerMode = cfg.BGPPeerMode
	meta.DiskDevice = cfg.DiskDevice
	if meta.Hostname == "" {
		if hostname, err := os.Hostname(); err == nil {
			meta.Hostname = hostname
		}
	}
	return meta
}

func marshalMetadata[T any](name string, fn func() (*T, error), errs *[]MetadataError) json.RawMessage {
	value, err := fn()
	if err != nil {
		*errs = append(*errs, MetadataError{Component: name, Error: err.Error()})
		return nil
	}
	return marshalValue(name, value, errs)
}

func marshalValue(name string, value any, errs *[]MetadataError) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		*errs = append(*errs, MetadataError{Component: name, Error: err.Error()})
		return nil
	}
	return data
}

func writeArchive(outputDir string, manifest *Manifest) (archivePath string, archiveBytes int64, err error) {
	if outputDir == "" {
		dir, err := os.MkdirTemp("", "booty-crash-*")
		if err != nil {
			return "", 0, fmt.Errorf("create crash archive dir: %w", err)
		}
		outputDir = dir
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("create crash archive dir: %w", err)
	}
	archivePath = filepath.Join(outputDir, "crash-artifacts.tar.gz")
	for range 4 {
		if err := writeArchiveFile(archivePath, manifest); err != nil {
			return "", 0, err
		}
		info, err := os.Stat(archivePath)
		if err != nil {
			return "", 0, fmt.Errorf("stat crash archive: %w", err)
		}
		if manifest.Scan.ArchiveBytes == info.Size() {
			return archivePath, info.Size(), nil
		}
		manifest.Scan.ArchiveBytes = info.Size()
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		return "", 0, fmt.Errorf("stat crash archive: %w", err)
	}
	return archivePath, info.Size(), nil
}

func writeArchiveFile(archivePath string, manifest *Manifest) error {
	out, err := os.Create(archivePath) //nolint:gosec // path is configured output directory
	if err != nil {
		return fmt.Errorf("create crash archive: %w", err)
	}
	defer out.Close() //nolint:errcheck // best-effort close on error path

	gz := gzip.NewWriter(out)
	gz.ModTime = manifest.CreatedAt
	defer gz.Close() //nolint:errcheck // close checked by caller through tar writes
	tw := tar.NewWriter(gz)
	defer tw.Close() //nolint:errcheck // close checked by caller through tar writes

	if err := addJSON(tw, "manifest.json", manifest); err != nil {
		return err
	}
	if err := addJSON(tw, "metadata.json", manifest.Metadata); err != nil {
		return err
	}
	for _, artifact := range manifest.Artifacts {
		if err := addFile(tw, artifact.SourcePath, artifact.ArchivePath); err != nil {
			slog.Warn("failed to add crash artifact", "path", artifact.SourcePath, "error", err)
		}
	}
	return nil
}

func addJSON(tw *tar.Writer, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC()}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write tar data %s: %w", name, err)
	}
	return nil
}

func addFile(tw *tar.Writer, source, archivePath string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("artifact is not a regular file")
	}
	header := &tar.Header{Name: archivePath, Mode: 0o600, Size: info.Size(), ModTime: info.ModTime()}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header: %w", err)
	}
	f, err := os.Open(source) //nolint:gosec // source is allowlisted and validated
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer f.Close() //nolint:errcheck // best-effort close
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("copy artifact: %w", err)
	}
	return nil
}
