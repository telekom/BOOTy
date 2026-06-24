//go:build linux

package main

import (
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/network"
)

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
