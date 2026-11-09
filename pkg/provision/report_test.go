//go:build linux

package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/tpm"
)

const secureBootVar = "SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c"

// withReportHost fakes the efivarfs and TPM that the report reads.
func withReportHost(t *testing.T, secureBoot []byte, tpmInfo *tpm.Info) {
	t.Helper()
	dir := t.TempDir()
	if secureBoot != nil {
		// efivarfs files start with 4 attribute bytes.
		if err := os.WriteFile(filepath.Join(dir, secureBootVar), append([]byte{7, 0, 0, 0}, secureBoot...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldDir, oldTPM, oldNow := reportEFIVarsDir, reportDetectTPM, reportNow
	reportEFIVarsDir = dir
	reportDetectTPM = func() *tpm.Info { return tpmInfo }
	reportNow = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	t.Cleanup(func() { reportEFIVarsDir, reportDetectTPM, reportNow = oldDir, oldTPM, oldNow })
}

func reportTestConfig() *config.MachineConfig {
	cfg := &config.MachineConfig{Hostname: "node-1"}
	cfg.Provision.Image.Mode = config.ImageModeAB
	cfg.Provision.Image.Checksum = strings.Repeat("ab", 32)
	cfg.Provision.Image.ChecksumType = "sha256"
	cfg.Provision.Image.SignatureURL = "https://images.example.invalid/node.raw.sig"
	cfg.Provision.AB.Scheme = config.ABSchemeSystemAB
	cfg.Provision.AB.ActiveSlot = "a"
	cfg.Provision.Sysext.Enabled = true
	cfg.Provision.Sysext.DefaultMode = "preload"
	cfg.Provision.Sysext.Layers = []config.SysextLayerConfig{
		{Name: "tools", Version: "1.2.3", SHA256: strings.Repeat("cd", 32)},
		{Name: "debug", Mode: "active"},
	}
	return cfg
}

func TestBuildReport(t *testing.T) {
	withReportHost(t, []byte{1}, &tpm.Info{Present: true, Version: "2.0"})
	cfg := reportTestConfig()
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	if err := o.ensureABPartitionLayout(); err != nil {
		t.Fatal(err)
	}
	o.bestImageURL = "https://user:secret@images.example.invalid/node.raw.zst?token=x"
	o.targetDisk = "/dev/vda"
	o.rootPartition = "/dev/vda3"
	if err := os.MkdirAll(filepath.Join(o.config.rootDir, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	crypttab := "# comment\n\ndata UUID=1234 none luks\n"
	if err := os.WriteFile(filepath.Join(o.config.rootDir, "etc", "crypttab"), []byte(crypttab), 0o600); err != nil {
		t.Fatal(err)
	}
	o.collector.Steps().StartStep("stream-image")
	o.collector.Steps().EndStep("stream-image", nil)

	r := o.buildReport()

	if r.Schema != ReportSchemaVersion || r.Hostname != "node-1" || r.Booty.Version == "" {
		t.Fatalf("header = %+v", r)
	}
	if strings.Contains(r.Image.Reference, "secret") || strings.Contains(r.Image.Reference, "token") {
		t.Fatalf("image reference not redacted: %q", r.Image.Reference)
	}
	if r.Image.Mode != config.ImageModeAB || r.Image.Checksum == nil || r.Image.Checksum.Scope != config.ChecksumScopeDownload {
		t.Fatalf("image = %+v", r.Image)
	}
	if r.Verification.Checksum != ReportStateVerified || r.Verification.Signature != (ReportSignatureResult{Method: "gpg", Result: ReportStateVerified}) {
		t.Fatalf("verification = %+v", r.Verification)
	}
	if r.AB == nil || r.AB.Scheme != config.ABSchemeSystemAB || r.AB.TargetSlot != "b" || r.AB.ActiveSlot != "a" {
		t.Fatalf("ab = %+v", r.AB)
	}
	if r.Disk.Device != "/dev/vda" || r.Disk.RootPartition != "/dev/vda3" || r.Disk.Layout == nil || len(r.Disk.Layout.Partitions) == 0 {
		t.Fatalf("disk = %+v", r.Disk)
	}
	if r.Encryption != (ReportEncryption{LUKS: ReportStatePresent, TPM: ReportStatePresent, TPMVersion: "2.0"}) {
		t.Fatalf("encryption = %+v", r.Encryption)
	}
	if r.SecureBoot.State != ReportStateEnabled {
		t.Fatalf("secure boot = %+v", r.SecureBoot)
	}
	wantSysexts := []ReportSysext{
		{Name: "tools", Version: "1.2.3", Mode: "preload", SHA256: strings.Repeat("cd", 32)},
		{Name: "debug", Mode: "active"},
	}
	if len(r.Sysexts) != 2 || r.Sysexts[0] != wantSysexts[0] || r.Sysexts[1] != wantSysexts[1] {
		t.Fatalf("sysexts = %+v", r.Sysexts)
	}
	if len(r.Timings.Steps) != 1 || r.Timings.Steps[0].Name != "stream-image" {
		t.Fatalf("timings = %+v", r.Timings)
	}
}

func TestBuildReportDefaults(t *testing.T) {
	withReportHost(t, nil, &tpm.Info{})
	o := newTestOrchestrator(t, &config.MachineConfig{}, &mockProvider{})
	o.bestImageURL = "oci://registry.example.invalid/node:1"
	o.imageDigest = "sha256:" + strings.Repeat("0", 64)

	r := o.buildReport()

	if r.Image.Mode != config.ImageModeWholeDisk || r.Image.Digest != o.imageDigest || r.Image.Checksum != nil {
		t.Fatalf("image = %+v", r.Image)
	}
	if r.Verification.Checksum != ReportStateNotConfigured || r.Verification.Signature.Result != ReportStateNotConfigured {
		t.Fatalf("verification = %+v", r.Verification)
	}
	if r.AB != nil || r.Sysexts != nil {
		t.Fatalf("ab/sysexts set without config: %+v %+v", r.AB, r.Sysexts)
	}
	if r.Encryption.LUKS != ReportStateAbsent || r.Encryption.TPM != ReportStateAbsent {
		t.Fatalf("encryption = %+v", r.Encryption)
	}
	if r.SecureBoot.State != ReportStateUnknown {
		t.Fatalf("secure boot without efivars = %q, want unknown", r.SecureBoot.State)
	}
}

func TestWriteProvisionReport(t *testing.T) {
	withReportHost(t, []byte{0}, nil)
	o := newTestOrchestrator(t, &config.MachineConfig{}, &mockProvider{})
	o.bestImageURL = "https://images.example.invalid/node.raw"

	if err := o.writeProvisionReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(o.config.rootDir, config.DefaultReportPath))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("report is not JSON: %v\n%s", err, data)
	}
	if got["schema"] != ReportSchemaVersion {
		t.Fatalf("schema = %v", got["schema"])
	}
	if sb, _ := got["secureBoot"].(map[string]any); sb["state"] != ReportStateDisabled {
		t.Fatalf("secureBoot = %v", got["secureBoot"])
	}
}

func TestWriteProvisionReportCustomPathAndNone(t *testing.T) {
	withReportHost(t, nil, nil)
	cfg := &config.MachineConfig{}
	cfg.Provision.ReportPath = "/var/lib/booty/report.json"
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	if err := o.writeProvisionReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(o.config.rootDir, "var", "lib", "booty", "report.json")); err != nil {
		t.Fatalf("custom report path: %v", err)
	}

	cfg = &config.MachineConfig{}
	cfg.Provision.ReportPath = config.ReportPathNone
	o = newTestOrchestrator(t, cfg, &mockProvider{})
	if err := o.writeProvisionReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	if o.report == nil {
		t.Fatal("report not built with reportPath none")
	}
	if _, err := os.Stat(filepath.Join(o.config.rootDir, config.DefaultReportPath)); !os.IsNotExist(err) {
		t.Fatalf("report written with reportPath none: %v", err)
	}
}

func TestWriteProvisionReportRefusesSymlinkOutOfRoot(t *testing.T) {
	withReportHost(t, nil, nil)
	o := newTestOrchestrator(t, &config.MachineConfig{}, &mockProvider{})
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(o.config.rootDir, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(o.config.rootDir, "etc", "booty")); err != nil {
		t.Fatal(err)
	}
	// Best effort: the step succeeds, the report still goes to the log.
	if err := o.writeProvisionReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	if o.report == nil {
		t.Fatal("report not built")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("files written outside the root: %v", entries)
	}
}

func TestWriteProvisionReportKeepsFileOnResume(t *testing.T) {
	withReportHost(t, nil, nil)
	o := newTestOrchestrator(t, &config.MachineConfig{}, &mockProvider{})
	path := filepath.Join(o.config.rootDir, config.DefaultReportPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("machine file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.reportWritten = true
	if err := o.writeProvisionReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	if o.report == nil {
		t.Fatal("report not rebuilt on resume")
	}
	if data, _ := os.ReadFile(path); string(data) != "machine file\n" {
		t.Fatalf("report file rewritten on resume: %q", data)
	}
}

func TestReportSuccessLogsReport(t *testing.T) {
	withReportHost(t, nil, nil)
	provider := &mockProvider{}
	o := newTestOrchestrator(t, &config.MachineConfig{}, provider)
	var buf bytes.Buffer
	o.log = slog.New(slog.NewJSONHandler(&buf, nil))
	cfg := o.cfg
	cfg.Provision.ReportPath = config.ReportPathNone
	if err := o.writeProvisionReport(context.Background()); err != nil {
		t.Fatal(err)
	}
	o.collector.Steps().StartStep("report-success")
	if err := o.reportSuccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	var line struct {
		Msg    string `json:"msg"`
		Report string `json:"report"`
	}
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if err := json.Unmarshal(raw, &line); err == nil && line.Msg == "provisioning report" {
			break
		}
	}
	if line.Msg != "provisioning report" {
		t.Fatalf("no report log line in:\n%s", buf.String())
	}
	var r ProvisionReport
	if err := json.Unmarshal([]byte(line.Report), &r); err != nil {
		t.Fatalf("logged report is not JSON: %v", err)
	}
	if len(r.Timings.Steps) == 0 || r.Timings.Steps[len(r.Timings.Steps)-1].Name != "report-success" {
		t.Fatalf("logged report has no final timings: %+v", r.Timings)
	}
	if len(provider.statuses) != 1 || provider.statuses[0].status != config.StatusSuccess {
		t.Fatalf("statuses = %+v", provider.statuses)
	}
}

func TestCrypttabState(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		content *string
		want    string
	}{
		"missing":  {nil, ReportStateAbsent},
		"comments": {new("# none\n\n"), ReportStateAbsent},
		"volume":   {new("data /dev/vdb2 none luks\n"), ReportStatePresent},
	} {
		path := filepath.Join(dir, name)
		if tc.content != nil {
			if err := os.WriteFile(path, []byte(*tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got := crypttabState(path); got != tc.want {
			t.Errorf("%s: crypttabState = %q, want %q", name, got, tc.want)
		}
	}
}

func TestWriteProvisionReportStepPlacement(t *testing.T) {
	o := newTestOrchestrator(t, &config.MachineConfig{}, &mockProvider{})
	steps := o.provisionSteps()
	idx := requireStepIndex(t, steps, "write-provision-report")
	if idx <= requireStepIndex(t, steps, "inject-cloudinit") || idx >= requireStepIndex(t, steps, "copy-machine-files") {
		t.Fatal("write-provision-report must run after inject-cloudinit and before copy-machine-files")
	}
	if _, ok := resumeStateSteps()["write-provision-report"]; !ok {
		t.Fatal("write-provision-report must rerun on resume: report-success needs the report in memory")
	}
}
