//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/network"
)

type teardownRecorderMode struct {
	teardown func() error
}

func (m teardownRecorderMode) Setup(context.Context, *network.Config) error { return nil }

func (m teardownRecorderMode) WaitForConnectivity(context.Context, string, time.Duration) error {
	return nil
}

func (m teardownRecorderMode) Teardown(context.Context) error {
	if m.teardown == nil {
		return nil
	}
	return m.teardown()
}

func TestMergeNetplanConfigPreservesProvisionPrefixForSameHostMask(t *testing.T) {
	dst := &network.Config{ProvisionIP: "10.200.0.10/24"}
	src := &network.Config{ProvisionIP: "10.200.0.10/32"}

	mergeNetplanConfig(dst, src)

	if dst.ProvisionIP != "10.200.0.10/24" {
		t.Fatalf("ProvisionIP = %q, want existing /24 prefix", dst.ProvisionIP)
	}
}

func TestMergeProvisionIPUsesDetectedNetworkPrefix(t *testing.T) {
	got := mergeProvisionIP("10.200.0.10/32", "10.200.0.10/24")

	if got != "10.200.0.10/24" {
		t.Fatalf("mergeProvisionIP() = %q, want detected /24", got)
	}
}

func TestMergeProvisionIPUsesDetectedDifferentHost(t *testing.T) {
	got := mergeProvisionIP("10.200.0.10/24", "10.200.0.11/32")

	if got != "10.200.0.11/32" {
		t.Fatalf("mergeProvisionIP() = %q, want detected different host", got)
	}
}

func TestMergeProvisionIPUsesDetectedInvalidCIDR(t *testing.T) {
	got := mergeProvisionIP("10.200.0.10/24", "not-a-cidr")

	if got != "not-a-cidr" {
		t.Fatalf("mergeProvisionIP() = %q, want detected invalid value", got)
	}
}

func TestMergeNetplanConfigOverridesStaticAddressPair(t *testing.T) {
	dst := &network.Config{StaticIP: "192.0.2.10/24", StaticIface: "eth9"}
	src := &network.Config{StaticIP: "10.1.2.3/24"}

	mergeNetplanConfig(dst, src)

	if dst.StaticIP != "10.1.2.3/24" {
		t.Fatalf("StaticIP = %q, want netplan address", dst.StaticIP)
	}
	if dst.StaticIface != "" {
		t.Fatalf("StaticIface = %q, want netplan auto-detect", dst.StaticIface)
	}
}

func TestPrepareLinkLayersCreatesBondBeforeVLAN(t *testing.T) {
	previousBond := setupBondLayer
	previousVLAN := setupVLANLayer
	var calls []string
	setupBondLayer = func(_ context.Context, _ *network.Config) (network.Mode, error) {
		calls = append(calls, "bond")
		return teardownRecorderMode{}, nil
	}
	setupVLANLayer = func(v network.VLANConfig) (string, error) {
		calls = append(calls, "vlan:"+v.Parent)
		return "bond0.100", nil
	}
	t.Cleanup(func() {
		setupBondLayer = previousBond
		setupVLANLayer = previousVLAN
	})

	cfg := &network.Config{
		BondInterfaces: "eth0,eth1",
		VLANs:          []network.VLANConfig{{ID: 100, Parent: "bond0", Address: "10.0.0.2/24"}},
	}
	if _, err := prepareLinkLayers(context.Background(), cfg); err != nil {
		t.Fatalf("prepareLinkLayers: %v", err)
	}

	want := []string{"bond", "vlan:bond0"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	}
	if cfg.StaticIface != "bond0.100" {
		t.Fatalf("StaticIface = %q, want bond0.100", cfg.StaticIface)
	}
}

func TestPrepareLinkLayersBondOnlySelectsBond0(t *testing.T) {
	previousBond := setupBondLayer
	setupBondLayer = func(_ context.Context, _ *network.Config) (network.Mode, error) {
		return teardownRecorderMode{}, nil
	}
	t.Cleanup(func() {
		setupBondLayer = previousBond
	})

	cfg := &network.Config{BondInterfaces: "eth0,eth1"}
	if _, err := prepareLinkLayers(context.Background(), cfg); err != nil {
		t.Fatalf("prepareLinkLayers: %v", err)
	}
	if cfg.StaticIface != "bond0" {
		t.Fatalf("StaticIface = %q, want bond0", cfg.StaticIface)
	}
}

func TestLinkLayerNetworkModeTeardownCleansInnerThenLinkLayers(t *testing.T) {
	previousVLAN := teardownVLANLayer
	var calls []string
	teardownVLANLayer = func(v network.VLANConfig) error {
		calls = append(calls, fmt.Sprintf("vlan:%s.%d", v.Parent, v.ID))
		return nil
	}
	t.Cleanup(func() { teardownVLANLayer = previousVLAN })

	inner := teardownRecorderMode{teardown: func() error {
		calls = append(calls, "inner")
		return nil
	}}
	cleanup := &linkLayerCleanup{
		bond: teardownRecorderMode{teardown: func() error {
			calls = append(calls, "bond")
			return nil
		}},
		vlans: []network.VLANConfig{{ID: 100, Parent: "bond0"}},
	}

	if err := wrapLinkLayerMode(inner, cleanup).Teardown(context.Background()); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	want := []string{"inner", "vlan:bond0.100", "bond"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestPrepareLinkLayersRollsBackOnVLANFailure(t *testing.T) {
	previousBond := setupBondLayer
	previousVLAN := setupVLANLayer
	previousTeardownVLAN := teardownVLANLayer
	var calls []string
	setupBondLayer = func(_ context.Context, _ *network.Config) (network.Mode, error) {
		calls = append(calls, "bond")
		return teardownRecorderMode{teardown: func() error {
			calls = append(calls, "teardown-bond")
			return nil
		}}, nil
	}
	setupVLANLayer = func(v network.VLANConfig) (string, error) {
		calls = append(calls, fmt.Sprintf("vlan:%d", v.ID))
		if v.ID == 200 {
			return "", fmt.Errorf("vlan setup failed")
		}
		return fmt.Sprintf("%s.%d", v.Parent, v.ID), nil
	}
	teardownVLANLayer = func(v network.VLANConfig) error {
		calls = append(calls, fmt.Sprintf("teardown-vlan:%d", v.ID))
		return nil
	}
	t.Cleanup(func() {
		setupBondLayer = previousBond
		setupVLANLayer = previousVLAN
		teardownVLANLayer = previousTeardownVLAN
	})

	cfg := &network.Config{
		BondInterfaces: "eth0,eth1",
		VLANs: []network.VLANConfig{
			{ID: 100, Parent: "bond0"},
			{ID: 200, Parent: "bond0"},
		},
	}
	if _, err := prepareLinkLayers(context.Background(), cfg); err == nil {
		t.Fatal("prepareLinkLayers() error = nil, want VLAN setup failure")
	}
	want := []string{"bond", "vlan:100", "vlan:200", "teardown-vlan:100", "teardown-bond"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestNetworkModeWithResolversCleansModeAndLinkLayersOnResolverFailure(t *testing.T) {
	previousTeardownVLAN := teardownVLANLayer
	var calls []string
	teardownVLANLayer = func(v network.VLANConfig) error {
		calls = append(calls, fmt.Sprintf("teardown-vlan:%d", v.ID))
		return nil
	}
	t.Cleanup(func() {
		teardownVLANLayer = previousTeardownVLAN
	})

	mode := teardownRecorderMode{teardown: func() error {
		calls = append(calls, "teardown-mode")
		return nil
	}}
	cleanup := &linkLayerCleanup{
		bond: teardownRecorderMode{teardown: func() error {
			calls = append(calls, "teardown-bond")
			return nil
		}},
		vlans: []network.VLANConfig{{ID: 100, Parent: "bond0"}},
	}
	netCfg := &network.Config{DNSResolvers: "8.8.8.8\nsearch evil.example"}

	got, err := networkModeWithResolvers(context.Background(), netCfg, mode, cleanup)
	if err == nil {
		t.Fatal("networkModeWithResolvers() error = nil, want resolver validation failure")
	}
	if got != nil {
		t.Fatalf("networkModeWithResolvers() mode = %T, want nil", got)
	}
	if !strings.Contains(err.Error(), "configure initramfs DNS") {
		t.Fatalf("networkModeWithResolvers() error = %q, want DNS context", err.Error())
	}

	want := []string{"teardown-mode", "teardown-vlan:100", "teardown-bond"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRequiresABKexec(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		preserve bool
		want     bool
	}{
		{name: "preserve existing A/B upgrade", mode: config.ImageModeAB, preserve: true, want: true},
		{name: "fresh A/B install", mode: config.ImageModeAB, preserve: false, want: false},
		{name: "whole disk ignores preserve flag", mode: config.ImageModeWholeDisk, preserve: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			cfg.Provision.Image.Mode = tt.mode
			cfg.Provision.AB.PreserveExisting = tt.preserve
			if got := requiresABKexec(cfg); got != tt.want {
				t.Fatalf("requiresABKexec() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestTryKexecSkipsWhenSecureBootReEnableRequested(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.SecureBoot.ReEnable = true

	if tryKexec(cfg, false) {
		t.Fatal("tryKexec returned true when secure boot re-enable requires hard reboot")
	}
}

func TestResolveKexecPath(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "boot", "vmlinuz-6.1.0"))
	mustWrite(t, filepath.Join(root, "boot", "initrd.img-6.1.0"))
	mustWrite(t, filepath.Join(root, "boot", "initramfs-6.1.0.img"))
	mustWrite(t, filepath.Join(root, "boot", "explicit-root-kernel"))
	mustWrite(t, filepath.Join(root, "vmlinuz-local"))
	mustWrite(t, filepath.Join(root, "boot", "vmlinuz-local"))
	if err := os.Mkdir(filepath.Join(root, "vmlinuz-directory"), 0o755); err != nil {
		t.Fatalf("mkdir root vmlinuz-directory: %v", err)
	}
	mustWrite(t, filepath.Join(root, "boot", "vmlinuz-directory"))

	tests := []struct {
		name     string
		grubPath string
		want     string
	}{
		{
			name:     "keeps explicit boot path",
			grubPath: "/boot/vmlinuz-6.1.0",
			want:     filepath.Join(root, "boot", "vmlinuz-6.1.0"),
		},
		{
			name:     "resolves root relative vmlinuz below mounted boot",
			grubPath: "/vmlinuz-6.1.0",
			want:     filepath.Join(root, "boot", "vmlinuz-6.1.0"),
		},
		{
			name:     "resolves root relative initrd below mounted boot",
			grubPath: "/initrd.img-6.1.0",
			want:     filepath.Join(root, "boot", "initrd.img-6.1.0"),
		},
		{
			name:     "resolves root relative initramfs below mounted boot",
			grubPath: "/initramfs-6.1.0.img",
			want:     filepath.Join(root, "boot", "initramfs-6.1.0.img"),
		},
		{
			name:     "prefers root path when it exists",
			grubPath: "/vmlinuz-local",
			want:     filepath.Join(root, "vmlinuz-local"),
		},
		{
			name:     "does not move non standard root relative path under boot",
			grubPath: "/explicit-root-kernel",
			want:     filepath.Join(root, "explicit-root-kernel"),
		},
		{
			name:     "skips root directory when boot file exists",
			grubPath: "/vmlinuz-directory",
			want:     filepath.Join(root, "boot", "vmlinuz-directory"),
		},
		{
			name:     "leaves empty path empty",
			grubPath: "  ",
			want:     "",
		},
		{
			name:     "does not move non boot artifact under boot",
			grubPath: "/EFI/BOOT/BOOTX64.EFI",
			want:     filepath.Join(root, "EFI", "BOOT", "BOOTX64.EFI"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveKexecPath(root, tt.grubPath); got != tt.want {
				t.Fatalf("resolveKexecPath(%q) = %q, want %q", tt.grubPath, got, tt.want)
			}
		})
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestSetupNetworkModeExplicitGoBGPFailsClosed(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Network.Mode = "gobgp"
	cfg.Network.BGP.ASN = 65000
	cfg.Network.BGP.UnderlayAF = "invalid"
	cfg.Network.EVPN.UnderlayIP = "10.0.0.1"
	cfg.Network.EVPN.ProvisionVNI = 4000
	cfg.Network.EVPN.ProvisionGateway = "10.0.0.254"

	mode, err := setupNetworkMode(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected explicit GoBGP setup error")
	}
	if mode != nil {
		t.Fatalf("mode = %T, want nil on explicit GoBGP setup failure", mode)
	}
	if !strings.Contains(err.Error(), "gobgp network setup") ||
		!strings.Contains(err.Error(), "invalid underlay AF") {
		t.Fatalf("error = %q, want GoBGP setup failure context", err.Error())
	}
}
