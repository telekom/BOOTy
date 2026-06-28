//go:build linux

package crash

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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

var (
	crashURLPattern = regexp.MustCompile(`https?://[^\s"'<>()]+`)
	secretPatterns  = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(\bauthorization\s*[:=]\s*bearer\s+)[^\s"']+`),
		regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:TOKEN|PASSWORD|SECRET|KEY)[A-Z0-9_]*\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;]+)`),
	}
)

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
	candidates, skipped = filterMemoryDumps(candidates, skipped, includeMemoryDumps(opts.Config))
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

	archivePath, archiveBytes, err := writeArchive(opts.OutputDir, &manifest, opts.MaxBytes)
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
	case isRawKdumpPath(lowerBase):
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

func filterMemoryDumps(candidates []candidate, skipped []SkippedArtifact, include bool) ([]candidate, []SkippedArtifact) {
	if include {
		return candidates, skipped
	}
	filtered := candidates[:0]
	for _, c := range candidates {
		if isMemoryDumpKind(c.kind) {
			skipped = append(skipped, SkippedArtifact{
				SourcePath: c.sourcePath,
				Reason:     "memory_dump_upload_disabled",
				SizeBytes:  c.size,
			})
			continue
		}
		filtered = append(filtered, c)
	}
	return filtered, skipped
}

func includeMemoryDumps(cfg *config.MachineConfig) bool {
	return cfg != nil && cfg.Provision.CrashArtifacts.IncludeMemoryDumps
}

func isMemoryDumpKind(kind string) bool {
	return kind == "kdump" || kind == "coredump"
}

func isRawKdumpPath(lowerBase string) bool {
	if strings.HasPrefix(lowerBase, "vmcore-dmesg") {
		return false
	}
	return lowerBase == "vmcore" || strings.HasPrefix(lowerBase, "vmcore.")
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
	sanitizeHostMetadata(&metadata)
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
	meta.ProviderID = cfg.Provision.ProviderID
	meta.Mode = cfg.Mode
	meta.Region = cfg.Provision.Region
	meta.FailureDomain = cfg.Provision.FailureDomain
	meta.ImageMode = cfg.Provision.Image.Mode
	meta.NetworkMode = cfg.Network.Mode
	meta.BGPPeerMode = cfg.Network.BGP.PeerMode
	meta.DiskDevice = cfg.Provision.Disk.Device
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

func writeArchive(outputDir string, manifest *Manifest, maxBytes int64) (archivePath string, archiveBytes int64, err error) {
	if outputDir == "" {
		dir, err := os.MkdirTemp("", "booty-crash-*")
		if err != nil {
			return "", 0, fmt.Errorf("create crash archive dir: %w", err)
		}
		outputDir = dir
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return "", 0, fmt.Errorf("create crash archive dir: %w", err)
	}
	if err := os.Chmod(outputDir, 0o700); err != nil { //nolint:gosec // owner-only directory requires execute bit
		return "", 0, fmt.Errorf("secure crash archive dir: %w", err)
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
		if err := enforceArchiveSize(archivePath, info.Size(), maxBytes); err != nil {
			return "", info.Size(), err
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
	if err := enforceArchiveSize(archivePath, info.Size(), maxBytes); err != nil {
		return "", info.Size(), err
	}
	return archivePath, info.Size(), nil
}

func enforceArchiveSize(archivePath string, archiveBytes, maxBytes int64) error {
	if maxBytes <= 0 || archiveBytes <= maxBytes {
		return nil
	}
	if err := os.Remove(archivePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove oversized crash archive: %w", err)
	}
	return fmt.Errorf("crash archive size %d exceeds limit %d", archiveBytes, maxBytes)
}

func writeArchiveFile(archivePath string, manifest *Manifest) error {
	if info, err := os.Lstat(archivePath); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("crash archive path is not a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat crash archive path: %w", err)
	}
	out, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // path is configured output directory
	if err != nil {
		return fmt.Errorf("create crash archive: %w", err)
	}
	if err := out.Chmod(0o600); err != nil {
		_ = out.Close()
		return fmt.Errorf("secure crash archive: %w", err)
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
	for i := range manifest.Artifacts {
		if err := addFile(tw, &manifest.Artifacts[i], filepath.Dir(archivePath)); err != nil {
			slog.Warn("failed to add crash artifact", "path", manifest.Artifacts[i].SourcePath, "error", err)
		}
	}
	return nil
}

func addJSON(tw *tar.Writer, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	data = redactCrashBytes(data)
	header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC()}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write tar data %s: %w", name, err)
	}
	return nil
}

func addFile(tw *tar.Writer, artifact *Artifact, scratchDir string) error {
	info, err := os.Stat(artifact.SourcePath)
	if err != nil {
		return fmt.Errorf("stat artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("artifact is not a regular file")
	}
	if shouldRedactArtifact(artifact.Kind) {
		return addRedactedFile(tw, artifact, info.ModTime(), scratchDir)
	}
	header := &tar.Header{Name: artifact.ArchivePath, Mode: 0o600, Size: info.Size(), ModTime: info.ModTime()}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header: %w", err)
	}
	f, err := os.Open(artifact.SourcePath) //nolint:gosec // source is allowlisted and validated
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer f.Close() //nolint:errcheck // best-effort close
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("copy artifact: %w", err)
	}
	return nil
}

func addRedactedFile(tw *tar.Writer, artifact *Artifact, modTime time.Time, scratchDir string) error {
	tmp, err := os.CreateTemp(scratchDir, ".booty-crash-redacted-*")
	if err != nil {
		return fmt.Errorf("create redacted artifact temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck // best-effort cleanup
	defer tmp.Close()        //nolint:errcheck // best-effort close on error path
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure redacted artifact temp file: %w", err)
	}
	if err := writeRedactedFile(tmp, artifact.SourcePath); err != nil {
		return err
	}
	info, err := tmp.Stat()
	if err != nil {
		return fmt.Errorf("stat redacted artifact temp file: %w", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind redacted artifact temp file: %w", err)
	}
	header := &tar.Header{Name: artifact.ArchivePath, Mode: 0o600, Size: info.Size(), ModTime: modTime}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header: %w", err)
	}
	if _, err := io.Copy(tw, tmp); err != nil {
		return fmt.Errorf("copy redacted artifact: %w", err)
	}
	return nil
}

func writeRedactedFile(w io.Writer, path string) error {
	return forEachRedactedChunk(path, func(chunk []byte) error {
		if _, err := w.Write(chunk); err != nil {
			return fmt.Errorf("write tar data: %w", err)
		}
		return nil
	})
}

func forEachRedactedChunk(path string, fn func([]byte) error) error {
	f, err := os.Open(path) //nolint:gosec // source is allowlisted and validated
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer f.Close() //nolint:errcheck // best-effort close

	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		chunk, err := reader.ReadBytes('\n')
		if len(chunk) > 0 {
			if err := fn(redactCrashBytes(chunk)); err != nil {
				return err
			}
		}
		if err == nil || errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		return fmt.Errorf("read artifact: %w", err)
	}
	return nil
}

func shouldRedactArtifact(kind string) bool {
	return kind == "kernel-log" || kind == "pstore"
}

func sanitizeHostMetadata(metadata *HostMetadata) {
	metadata.Machine.ProviderID = redactCrashString(metadata.Machine.ProviderID)
	metadata.Inventory = redactRawMessage(metadata.Inventory)
	metadata.Firmware = redactRawMessage(metadata.Firmware)
	metadata.Debug = redactRawMessage(metadata.Debug)
	metadata.BuildInfo = redactRawMessage(metadata.BuildInfo)
	for i := range metadata.Errors {
		metadata.Errors[i].Error = redactCrashString(metadata.Errors[i].Error)
	}
}

func redactRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return json.RawMessage(redactCrashBytes(raw))
	}
	redacted, ok := redactJSONValue(value)
	if !ok {
		return json.RawMessage(redactCrashBytes(raw))
	}
	data, err := json.Marshal(redacted)
	if err != nil {
		return json.RawMessage(redactCrashBytes(raw))
	}
	return json.RawMessage(data)
}

func redactJSONValue(value any) (any, bool) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if isSecretKey(key) {
				v[key] = "[REDACTED]"
				continue
			}
			redacted, ok := redactJSONValue(child)
			if !ok {
				return nil, false
			}
			v[key] = redacted
		}
		return v, true
	case []any:
		for i, child := range v {
			redacted, ok := redactJSONValue(child)
			if !ok {
				return nil, false
			}
			v[i] = redacted
		}
		return v, true
	case string:
		return redactCrashString(v), true
	case nil, bool, float64:
		return v, true
	default:
		return nil, false
	}
}

func isSecretKey(key string) bool {
	upper := strings.ToUpper(key)
	return strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "PASSWORD") ||
		strings.Contains(upper, "SECRET") ||
		strings.Contains(upper, "KEY") ||
		strings.Contains(upper, "AUTHORIZATION")
}

func redactCrashBytes(data []byte) []byte {
	return []byte(redactCrashString(string(data)))
}

func redactCrashString(value string) string {
	value = crashURLPattern.ReplaceAllStringFunc(value, redactCrashURL)
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllString(value, "${1}[REDACTED]")
	}
	return value
}

func redactCrashURL(raw string) string {
	candidate, suffix := splitURLSuffix(raw)
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String() + suffix
}

func splitURLSuffix(raw string) (candidate, suffix string) {
	candidate = raw
	for candidate != "" && strings.ContainsRune(".,;:)]}", rune(candidate[len(candidate)-1])) {
		suffix = candidate[len(candidate)-1:] + suffix
		candidate = candidate[:len(candidate)-1]
	}
	return candidate, suffix
}
