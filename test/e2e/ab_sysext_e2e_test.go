//go:build e2e && linux

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
	"github.com/telekom/BOOTy/pkg/provision"
)

func TestABSysextVarsRoundTripE2E(t *testing.T) {
	layers := `[
		{"name":"node-tuning","version":"2026.06.06","source":"oci://registry.example.invalid/tcaas/node-tuning:2026.06.06","fileName":"node-tuning.raw","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"name":"vsr","version":"1.0.0","source":"https://images.example.invalid/vsr.raw","fileName":"vsr.raw","sha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","mode":"preload"}
	]`
	vars := strings.Join([]string{
		`export IMAGE="https://images.example.invalid/os.raw.gz"`,
		`export IMAGE_MODE="ab"`,
		`export IMAGE_CHECKSUM="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"`,
		`export IMAGE_CHECKSUM_TYPE="sha256"`,
		`export AB_SCHEME="dual-root"`,
		`export AB_ACTIVE_SLOT="a"`,
		`export AB_TARGET_SLOT="inactive"`,
		`export AB_PRESERVE_EXISTING="true"`,
		`export AB_BOOT_SIZE_MB="1024"`,
		`export AB_ROOT_SIZE_MB="65536"`,
		`export AB_STATE_SIZE_MB="16384"`,
		`export SYSEXT_ENABLED="true"`,
		`export SYSEXT_DEFAULT_MODE="preload"`,
		`export SYSEXT_CATALOG_DIR="/usr/lib/tcaas-sysext/preloaded"`,
		`export SYSEXT_ACTIVE_DIR="/var/lib/extensions"`,
		"export SYSEXT_LAYERS=" + strconv.Quote(layers),
	}, "\n")

	cfg, err := parseTestVars(vars)
	if err != nil {
		t.Fatalf("ParseVars() error: %v", err)
	}
	if cfg.Provision.Image.Mode != config.ImageModeAB {
		t.Fatalf("image mode = %q, want %q", cfg.Provision.Image.Mode, config.ImageModeAB)
	}
	if cfg.Provision.AB.Scheme != config.ABSchemeDualRoot {
		t.Fatalf("A/B scheme = %q, want dual-root", cfg.Provision.AB.Scheme)
	}
	if cfg.Provision.AB.ActiveSlot != config.ABSlotA {
		t.Fatalf("A/B active slot = %q, want a", cfg.Provision.AB.ActiveSlot)
	}
	target, err := cfg.Provision.AB.ResolvedTargetSlot()
	if err != nil {
		t.Fatalf("ResolvedTargetSlot() error: %v", err)
	}
	if target != config.ABSlotB {
		t.Fatalf("resolved target slot = %q, want b", target)
	}
	if !cfg.Provision.AB.PreserveExisting {
		t.Fatal("A/B preserveExisting should be true")
	}
	if cfg.Provision.AB.BootSizeMB != 1024 || cfg.Provision.AB.RootSizeMB != 65536 || cfg.Provision.AB.StateSizeMB != 16384 {
		t.Fatalf("unexpected A/B sizes: %+v", cfg.Provision.AB)
	}
	if !cfg.Provision.Sysext.Enabled {
		t.Fatal("sysext should be enabled")
	}
	if cfg.Provision.Sysext.DefaultMode != "preload" {
		t.Fatalf("sysext default mode = %q, want preload", cfg.Provision.Sysext.DefaultMode)
	}
	if len(cfg.Provision.Sysext.Layers) != 2 {
		t.Fatalf("sysext layers = %d, want 2", len(cfg.Provision.Sysext.Layers))
	}
	if cfg.Provision.Sysext.Layers[0].Source != "oci://registry.example.invalid/tcaas/node-tuning:2026.06.06" {
		t.Fatalf("first sysext source = %q", cfg.Provision.Sysext.Layers[0].Source)
	}
}

func TestABSystemVarsRoundTripE2E(t *testing.T) {
	dataPartitions := `[
		{"label":"BOOTY-VAR","mountpoint":"/var","sizeMB":8192},
		{"label":"BOOTY-HOME","mountpoint":"/home"}
	]`
	vars := strings.Join([]string{
		`export IMAGE="https://images.example.invalid/os.raw.gz"`,
		`export IMAGE_MODE="ab"`,
		`export AB_SCHEME="system-ab"`,
		`export AB_TARGET_SLOT="a"`,
		"export AB_DATA_PARTITIONS=" + strconv.Quote(dataPartitions),
	}, "\n")

	cfg, err := parseTestVars(vars)
	if err != nil {
		t.Fatalf("ParseVars() error: %v", err)
	}
	if cfg.Provision.AB.Scheme != config.ABSchemeSystemAB {
		t.Fatalf("A/B scheme = %q, want system-ab", cfg.Provision.AB.Scheme)
	}
	if len(cfg.Provision.AB.DataPartitions) != 2 {
		t.Fatalf("data partitions = %d, want 2", len(cfg.Provision.AB.DataPartitions))
	}
	if cfg.Provision.AB.DataPartitions[0].Mountpoint != "/var" {
		t.Fatalf("first data partition = %+v", cfg.Provision.AB.DataPartitions[0])
	}
}

func TestABSysextVarsRejectBrokenInputsE2E(t *testing.T) {
	cases := []struct {
		name    string
		vars    string
		wantErr string
	}{
		{
			name: "invalid active slot",
			vars: strings.Join([]string{
				`export IMAGE="https://images.example.invalid/os.raw"`,
				`export IMAGE_MODE="ab"`,
				`export AB_ACTIVE_SLOT="blue"`,
			}, "\n"),
			wantErr: "invalid provision.ab.activeSlot",
		},
		{
			name: "invalid target slot",
			vars: strings.Join([]string{
				`export IMAGE="https://images.example.invalid/os.raw"`,
				`export IMAGE_MODE="ab"`,
				`export AB_TARGET_SLOT="next"`,
			}, "\n"),
			wantErr: "invalid provision.ab.targetSlot",
		},
		{
			name: "preserve existing without ab mode",
			vars: strings.Join([]string{
				`export IMAGE="https://images.example.invalid/os.raw"`,
				`export IMAGE_MODE="whole-disk"`,
				`export AB_PRESERVE_EXISTING="true"`,
			}, "\n"),
			wantErr: "provision.ab.preserveExisting requires provision.image.mode=ab",
		},
		{
			name: "negative root size",
			vars: strings.Join([]string{
				`export IMAGE="https://images.example.invalid/os.raw"`,
				`export IMAGE_MODE="ab"`,
				`export AB_ROOT_SIZE_MB="-1"`,
			}, "\n"),
			wantErr: "provision.ab.rootSizeMB must be non-negative",
		},
		{
			name: "invalid sysext json",
			vars: strings.Join([]string{
				`export IMAGE="https://images.example.invalid/os.raw"`,
				`export SYSEXT_LAYERS="not-json"`,
			}, "\n"),
			wantErr: "invalid SYSEXT_LAYERS",
		},
		{
			name: "enabled sysext without source",
			vars: strings.Join([]string{
				`export IMAGE="https://images.example.invalid/os.raw"`,
				`export SYSEXT_ENABLED="true"`,
				`export SYSEXT_LAYERS="[{\"name\":\"debug\"}]"`,
			}, "\n"),
			wantErr: "source is required",
		},
		{
			name: "unsafe sysext file name",
			vars: strings.Join([]string{
				`export IMAGE="https://images.example.invalid/os.raw"`,
				`export SYSEXT_LAYERS="[{\"name\":\"debug\",\"source\":\"https://images.example.invalid/debug.raw\",\"fileName\":\"../debug.raw\"}]"`,
			}, "\n"),
			wantErr: "provision.sysext.layers[0].fileName",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseTestVars(tc.vars)
			if err == nil {
				t.Fatal("ParseVars() succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ParseVars() error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestABPartitionLayoutContractE2E(t *testing.T) {
	ab := config.ABConfig{
		ActiveSlot:  config.ABSlotA,
		TargetSlot:  config.ABTargetInactive,
		BootSizeMB:  768,
		RootSizeMB:  4096,
		StateSizeMB: 2048,
	}

	layout, err := ab.PartitionLayout("/dev/vda")
	if err != nil {
		t.Fatalf("PartitionLayout() error: %v", err)
	}
	if layout.Device != "/dev/vda" {
		t.Fatalf("layout device = %q, want /dev/vda", layout.Device)
	}
	if len(layout.Partitions) != 4 {
		t.Fatalf("partitions = %d, want 4", len(layout.Partitions))
	}
	want := []struct {
		label      string
		sizeMB     int
		filesystem string
		mountpoint string
	}{
		{"BOOTY-EFI", 768, "vfat", "/boot/efi"},
		{"BOOTY-ROOT-A", 4096, "ext4", ""},
		{"BOOTY-ROOT-B", 4096, "ext4", "/"},
		{"BOOTY-STATE", 2048, "ext4", "/var/lib/booty"},
	}
	for i, wantPart := range want {
		got := layout.Partitions[i]
		if got.Label != wantPart.label || got.SizeMB != wantPart.sizeMB || got.Filesystem != wantPart.filesystem || got.Mountpoint != wantPart.mountpoint {
			t.Fatalf("partition[%d] = %+v, want %+v", i, got, wantPart)
		}
	}

	explicit, err := (&config.ABConfig{ActiveSlot: config.ABSlotB, TargetSlot: config.ABSlotA}).PartitionLayout("/dev/vda")
	if err != nil {
		t.Fatalf("explicit PartitionLayout() error: %v", err)
	}
	if explicit.Partitions[1].Mountpoint != "/" || explicit.Partitions[2].Mountpoint != "" {
		t.Fatalf("explicit target A mountpoints = %q/%q, want / and empty", explicit.Partitions[1].Mountpoint, explicit.Partitions[2].Mountpoint)
	}
}

func TestABSystemPartitionLayoutContractE2E(t *testing.T) {
	ab := config.ABConfig{
		Scheme:     config.ABSchemeSystemAB,
		TargetSlot: config.ABSlotA,
		BootSizeMB: 128,
		RootSizeMB: 1024,
		DataPartitions: []config.ABDataPartition{
			{Label: "BOOTY-VAR", SizeMB: 4096, Mountpoint: "/var"},
			{Label: "BOOTY-HOME", Mountpoint: "/home"},
		},
	}

	layout, err := ab.PartitionLayout("/dev/vda")
	if err != nil {
		t.Fatalf("PartitionLayout() error: %v", err)
	}
	want := []struct {
		label      string
		sizeMB     int
		filesystem string
		mountpoint string
	}{
		{"BOOTY-EFI", 128, "vfat", "/boot/efi"},
		{"BOOTY-ROOT-A", 1024, "ext4", "/"},
		{"BOOTY-ROOT-B", 1024, "ext4", ""},
		{"BOOTY-VAR", 4096, "ext4", "/var"},
		{"BOOTY-HOME", 0, "ext4", "/home"},
	}
	if len(layout.Partitions) != len(want) {
		t.Fatalf("partitions = %d, want %d", len(layout.Partitions), len(want))
	}
	for i, wantPart := range want {
		got := layout.Partitions[i]
		if got.Label != wantPart.label || got.SizeMB != wantPart.sizeMB || got.Filesystem != wantPart.filesystem || got.Mountpoint != wantPart.mountpoint {
			t.Fatalf("partition[%d] = %+v, want %+v", i, got, wantPart)
		}
	}
}

func TestSysextPreloadIsLoadedButNotEnabledOnDiskE2E(t *testing.T) {
	root := t.TempDir()
	preloadSource, preloadDigest := writeE2ESysextSource(t, "node tuning sysext")
	activeSource, activeDigest := writeE2ESysextSource(t, "debug sysext")

	cfg := config.SysextConfig{
		Enabled:     true,
		DefaultMode: "preload",
		Layers: []config.SysextLayerConfig{
			{
				Name:     "node-tuning",
				Version:  "2026.06.06",
				Source:   preloadSource,
				FileName: "node-tuning.raw",
				SHA256:   preloadDigest,
			},
			{
				Name:     "debug-tools",
				Version:  "1.0.0",
				Source:   activeSource,
				FileName: "debug-tools.raw",
				SHA256:   "sha256:" + activeDigest,
				Mode:     "active",
			},
		},
	}
	if err := (&config.Config{Provision: config.ProvisionConfig{Sysext: cfg}}).Validate(); err != nil {
		t.Fatalf("sysext config should validate: %v", err)
	}

	c := provision.NewConfigurator(disk.NewManager(newMockCommander()))
	c.SetRootDir(root)
	if err := c.ApplySysexts(context.Background(), &cfg); err != nil {
		t.Fatalf("ApplySysexts() error: %v", err)
	}

	preloadedPath := filepath.Join(root, "usr/lib/tcaas-sysext/preloaded/node-tuning.raw")
	activePath := filepath.Join(root, "var/lib/extensions/debug-tools.raw")
	preloadedActivePath := filepath.Join(root, "var/lib/extensions/node-tuning.raw")
	for _, path := range []string{preloadedPath, activePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected sysext file %s: %v", path, err)
		}
	}
	if _, err := os.Stat(preloadedActivePath); !os.IsNotExist(err) {
		t.Fatalf("preloaded sysext must not be enabled at %s; stat err=%v", preloadedActivePath, err)
	}

	catalogPath := filepath.Join(root, "usr/lib/tcaas-sysext/preloaded/catalog.json")
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	var catalog struct {
		Kind   string `json:"kind"`
		Layers []struct {
			Name     string `json:"name"`
			Version  string `json:"version"`
			FileName string `json:"fileName"`
			Path     string `json:"path"`
			Digest   string `json:"digest"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("parse catalog: %v", err)
	}
	if catalog.Kind != "SysextPreloadCatalog" {
		t.Fatalf("catalog kind = %q, want SysextPreloadCatalog", catalog.Kind)
	}
	if len(catalog.Layers) != 1 {
		t.Fatalf("catalog layers = %d, want 1: %s", len(catalog.Layers), data)
	}
	layer := catalog.Layers[0]
	if layer.Name != "node-tuning" || layer.FileName != "node-tuning.raw" || layer.Path != "/usr/lib/tcaas-sysext/preloaded/node-tuning.raw" {
		t.Fatalf("unexpected catalog layer: %+v", layer)
	}
	if layer.Digest != "sha256:"+preloadDigest {
		t.Fatalf("catalog digest = %q, want sha256:%s", layer.Digest, preloadDigest)
	}
	if strings.Contains(string(data), "debug-tools") {
		t.Fatalf("active sysext should not be written to preload catalog: %s", data)
	}
}

func writeE2ESysextSource(t *testing.T, content string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), fmt.Sprintf("%x.raw", sha256.Sum256([]byte(content))))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write sysext source: %v", err)
	}
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}
