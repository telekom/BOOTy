//go:build linux

package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/telekom/BOOTy/pkg/buildinfo"
	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/efi"
	"github.com/telekom/BOOTy/pkg/image"
	"github.com/telekom/BOOTy/pkg/telemetry"
	"github.com/telekom/BOOTy/pkg/tpm"
)

// ReportSchemaVersion is the version of the provisioning report format.
// Adding a field keeps the version; renaming, removing or changing the
// meaning of a field raises it.
const ReportSchemaVersion = "booty.provision-report/v1"

// Values of the report state fields.
const (
	ReportStateVerified      = "verified"
	ReportStateNotConfigured = "not-configured"
	ReportStateEnabled       = "enabled"
	ReportStateDisabled      = "disabled"
	ReportStatePresent       = "present"
	ReportStateAbsent        = "absent"
	ReportStateUnknown       = "unknown"
)

var (
	reportEFIVarsDir = "" // efivarfs; "" is the system default
	reportDetectTPM  = tpm.Detect
	reportNow        = time.Now
)

// ProvisionReport is the machine-readable result of a successful provision.
// See docs/provisioning-report.md.
type ProvisionReport struct {
	Schema       string             `json:"schema"`
	GeneratedAt  time.Time          `json:"generatedAt"`
	Hostname     string             `json:"hostname,omitempty"`
	Booty        ReportBooty        `json:"booty"`
	Image        ReportImage        `json:"image"`
	Verification ReportVerification `json:"verification"`
	Disk         ReportDisk         `json:"disk"`
	AB           *ReportAB          `json:"ab,omitempty"`
	Encryption   ReportEncryption   `json:"encryption"`
	SecureBoot   ReportSecureBoot   `json:"secureBoot"`
	Sysexts      []ReportSysext     `json:"sysexts,omitempty"`
	Timings      ReportTimings      `json:"timings"`
}

// ReportBooty identifies the BOOTy build that provisioned the host.
type ReportBooty struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Flavor  string `json:"flavor"`
}

// ReportImage is the image source. Reference has credentials, query and
// fragment removed.
type ReportImage struct {
	Reference string `json:"reference"`
	// Digest is the OCI manifest digest that verify-image resolved.
	Digest   string          `json:"digest,omitempty"`
	Mode     string          `json:"mode"`
	Checksum *ReportChecksum `json:"checksum,omitempty"`
}

// ReportChecksum is the configured image checksum.
type ReportChecksum struct {
	Algorithm string `json:"algorithm,omitempty"`
	Value     string `json:"value"`
	Scope     string `json:"scope"`
}

// ReportVerification holds the result of each image check. A provision
// that reports success passed every configured check.
type ReportVerification struct {
	Checksum  string                `json:"checksum"`
	Signature ReportSignatureResult `json:"signature"`
}

// ReportSignatureResult is the image signature check. Method is empty when
// no signature is configured; "gpg" is the detached GPG signature.
type ReportSignatureResult struct {
	Method string `json:"method,omitempty"`
	Result string `json:"result"`
}

// ReportDisk is the applied storage layout.
type ReportDisk struct {
	Device        string                  `json:"device,omitempty"`
	RootPartition string                  `json:"rootPartition,omitempty"`
	Layout        *config.PartitionLayout `json:"layout,omitempty"`
}

// ReportAB is the A/B slot state, also written to /etc/booty/ab-slot.env.
type ReportAB struct {
	Scheme           string `json:"scheme"`
	TargetSlot       string `json:"targetSlot"`
	ActiveSlot       string `json:"activeSlot,omitempty"`
	PreserveExisting bool   `json:"preserveExisting"`
}

// ReportEncryption is the disk encryption and TPM state.
type ReportEncryption struct {
	// LUKS is "present" when the installed /etc/crypttab names a volume.
	LUKS       string `json:"luks"`
	TPM        string `json:"tpm"`
	TPMVersion string `json:"tpmVersion,omitempty"`
}

// ReportSecureBoot is the firmware Secure Boot state during provisioning.
type ReportSecureBoot struct {
	State    string `json:"state"`
	ReEnable bool   `json:"reEnable,omitempty"`
	// UKI is the signed UKI of DISK_ENCRYPTION=luks2-tpm2-uki.
	UKI *ReportUKI `json:"uki,omitempty"`
}

// ReportSysext is one system extension that apply-sysexts installed.
type ReportSysext struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Mode    string `json:"mode,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}

// ReportTimings are the step timings up to the report.
type ReportTimings struct {
	StartTime       time.Time               `json:"startTime"`
	TotalDurationMs int64                   `json:"totalDurationMs"`
	Steps           []telemetry.StepMetrics `json:"steps"`
}

// buildReport collects the report. It reads the target root, so it runs
// while the root is mounted.
func (o *Orchestrator) buildReport(ctx context.Context) *ProvisionReport {
	info := buildinfo.Get()
	cfg := &o.cfg.Provision
	r := &ProvisionReport{
		Schema:      ReportSchemaVersion,
		GeneratedAt: reportNow().UTC(),
		Hostname:    o.cfg.Hostname,
		Booty:       ReportBooty{Version: info.Version, Commit: info.Commit, Flavor: string(info.Flavor)},
		Image:       o.reportImage(),
		Verification: ReportVerification{
			Checksum:  ReportStateNotConfigured,
			Signature: ReportSignatureResult{Result: ReportStateNotConfigured},
		},
		Disk:       ReportDisk{Device: o.targetDisk, RootPartition: o.rootPartition, Layout: o.reportLayout(ctx)},
		Encryption: reportEncryption(o.config.rootDir),
		SecureBoot: ReportSecureBoot{State: reportSecureBootState(), ReEnable: cfg.SecureBoot.ReEnable, UKI: o.reportUKI()},
		Sysexts:    reportSysexts(&cfg.Sysext),
	}
	if strings.TrimSpace(cfg.Image.Checksum) != "" {
		r.Verification.Checksum = ReportStateVerified
	}
	switch {
	case len(cfg.Image.CosignPublicKeys) > 0 && image.IsOCIReference(o.bestImageURL):
		r.Verification.Signature = ReportSignatureResult{Method: "cosign", Result: ReportStateVerified}
	case cfg.Image.SignatureURL != "":
		r.Verification.Signature = ReportSignatureResult{Method: "gpg", Result: ReportStateVerified}
	}
	if o.isABImageMode() {
		ab := cfg.AB.WithDefaults()
		slot, _ := ab.ResolvedTargetSlot() // validated before the disk was written
		r.AB = &ReportAB{Scheme: ab.Scheme, TargetSlot: slot, ActiveSlot: ab.ActiveSlot, PreserveExisting: ab.PreserveExisting}
	}
	o.refreshReportTimings(r)
	return r
}

func (o *Orchestrator) refreshReportTimings(r *ProvisionReport) {
	s := o.collector.Summarize()
	// A step error is free text that can hold command output (a rescue
	// retry keeps the failures of the earlier attempt); the report keeps
	// only the status and the stable failure code.
	steps := slices.Clone(s.Steps)
	for i := range steps {
		steps[i].Error = ""
	}
	r.Timings = ReportTimings{StartTime: s.StartTime.UTC(), TotalDurationMs: s.TotalDurMs, Steps: steps}
}

// reportLayout is the layout that write-fstab applied: a custom layout
// with the root filesystem that the streamed image has. When that can not
// be detected, it is the configured layout.
func (o *Orchestrator) reportLayout(ctx context.Context) *config.PartitionLayout {
	layout := o.cfg.Provision.Disk.PartitionLayout
	if !o.shouldDetectPartitionLayoutRootFilesystem() {
		return layout
	}
	detected, err := o.partitionLayoutWithDetectedRootFilesystem(ctx, layout)
	if err != nil {
		o.log.Warn("provisioning report: reporting the configured partition layout", "error", err)
		return layout
	}
	return detected
}

func (o *Orchestrator) reportImage() ReportImage {
	cfg := &o.cfg.Provision.Image
	mode := strings.TrimSpace(cfg.Mode)
	if mode == "" {
		mode = config.ImageModeWholeDisk
	}
	img := ReportImage{Reference: image.RedactURL(o.bestImageURL), Digest: o.imageDigest, Mode: mode}
	if sum := strings.TrimSpace(cfg.Checksum); sum != "" {
		scope := cfg.ChecksumScope
		if scope == "" {
			scope = config.ChecksumScopeDownload
		}
		img.Checksum = &ReportChecksum{Algorithm: cfg.ChecksumType, Value: sum, Scope: scope}
	}
	return img
}

func reportEncryption(root string) ReportEncryption {
	enc := ReportEncryption{LUKS: crypttabState(root), TPM: ReportStateAbsent}
	if info := reportDetectTPM(); info != nil && info.Present {
		enc.TPM = ReportStatePresent
		enc.TPMVersion = info.Version
	}
	return enc
}

// maxCrypttabBytes bounds the crypttab read; a real one is a few lines.
const maxCrypttabBytes = 64 * 1024

// crypttabState is "present" when the installed /etc/crypttab names at
// least one volume. The file comes from the image, so a symlink, a
// non-regular file, a path that leaves root or an oversized file is
// "unknown": it is neither followed nor read without bound.
func crypttabState(root string) string {
	path := filepath.Join(root, "etc", "crypttab")
	if err := ensureTargetParentWithinRoot(root, filepath.Dir(path)); err != nil {
		return ReportStateUnknown
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ReportStateAbsent
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCrypttabBytes {
		return ReportStateUnknown
	}
	// O_NOFOLLOW and O_NONBLOCK keep a file swapped after the Lstat from
	// being followed or blocking the open; Stat checks it again.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // path checked below the target root
	if err != nil {
		return ReportStateUnknown
	}
	defer func() { _ = f.Close() }()
	if info, err = f.Stat(); err != nil || !info.Mode().IsRegular() {
		return ReportStateUnknown
	}
	data, err := io.ReadAll(io.LimitReader(f, maxCrypttabBytes+1))
	if err != nil || len(data) > maxCrypttabBytes {
		return ReportStateUnknown
	}
	for line := range strings.Lines(string(data)) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			return ReportStatePresent
		}
	}
	return ReportStateAbsent
}

func reportSecureBootState() string {
	enabled, err := efi.NewEFIVarReader(reportEFIVarsDir).IsSecureBootEnabled()
	switch {
	case err != nil:
		return ReportStateUnknown
	case enabled:
		return ReportStateEnabled
	}
	return ReportStateDisabled
}

func reportSysexts(cfg *config.SysextConfig) []ReportSysext {
	if !cfg.Enabled {
		return nil
	}
	out := make([]ReportSysext, 0, len(cfg.Layers))
	for i := range cfg.Layers {
		l := &cfg.Layers[i]
		out = append(out, ReportSysext{Name: l.Name, Version: l.Version, Mode: sysextLayerMode(cfg, l), SHA256: l.SHA256})
	}
	return out
}

// writeProvisionReport builds the report and writes it into the installed
// OS at provision.reportPath, unless that is "none". The file is best
// effort, like the other reports: a failed write logs a warning and the
// report still reaches LOG_URL through report-success.
func (o *Orchestrator) writeProvisionReport(ctx context.Context) error {
	// A new report (a rescue retry runs the steps again) is logged again.
	o.report, o.reportLogged = o.buildReport(ctx), false
	path := o.cfg.Provision.ReportTargetPath()
	switch {
	case path == "":
		o.log.Info("provisioning report not written to the target", "reportPath", config.ReportPathNone)
		return nil
	case o.reportWritten:
		o.log.Info("provisioning report already written by the interrupted run", "path", path)
		return nil
	}
	data, err := json.MarshalIndent(o.report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal provisioning report: %w", err)
	}
	if err := writeTargetRootFile(o.config.rootDir, filepath.Join(o.config.rootDir, path), append(data, '\n')); err != nil {
		o.log.Warn("provisioning report not written to the target", "path", path, "error", err)
	}
	return nil
}

// logProvisionReport sends the report with the final step timings as one
// log line, which LOG_URL receives as "[INFO] provisioning report ...
// report=<json>".
func (o *Orchestrator) logProvisionReport() {
	if o.report == nil || o.reportLogged {
		return
	}
	o.refreshReportTimings(o.report)
	data, err := json.Marshal(o.report)
	if err != nil {
		o.log.Warn("marshal provisioning report", "error", err)
		return
	}
	o.log.Info("provisioning report", "report", string(data))
	o.reportLogged = true
}
