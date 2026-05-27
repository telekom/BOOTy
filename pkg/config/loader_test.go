package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadYAML(t *testing.T) {
	content := `
hostname: test-node-01
mode: provision

network:
  mode: gobgp
  dnsResolvers: "8.8.8.8,8.8.4.4"
  bgp:
    asn: 65001
    peerMode: unnumbered
    minPeers: 2
    keepalive: 30
    hold: 90
  evpn:
    underlaySubnet: "192.168.4.0/24"
    provisionVNI: 100
    provisionIP: "10.100.0.20/24"
  static:
    ip: ""
  bond:
    interfaces: "eth0,eth1"
    mode: "802.3ad"
  vrf:
    name: "Vrf_underlay"
    tableID: 1000
  ipmi:
    subnet: "172.30.0.0/24"

transport:
  token: "bootstrap-token-xyz"
  tokenURL: "https://caprf.example.com/token"
  tokenAlgorithm: RS256
  initURL: "https://caprf.example.com/status/init"
  successURL: "https://caprf.example.com/status/success"
  errorURL: "https://caprf.example.com/status/error"
  logURL: "https://caprf.example.com/logs"

health:
  enabled: true
  minMemoryGB: 16
  minCPUs: 4
  skipChecks: "thermal"
  reportURL: "https://caprf.example.com/health"

telemetry:
  enabled: true
  url: "https://caprf.example.com/telemetry"
  metricsURL: "https://caprf.example.com/metrics"
  eventURL: "https://caprf.example.com/events"

rescue:
  mode: retry
  sshPubKey: "ssh-ed25519 AAAAC3... admin@ops"
  timeout: 300

provision:
  extraKernelParams: "console=ttyS0,115200"
  failureDomain: zone-a
  region: eu-west-1
  providerID: "metal://node-01"
  postProvisionCmds:
    - "systemctl enable kubelet"
    - "kubeadm init"
  image:
    urls:
      - "https://registry.example.com/os-image:v1.2.3"
    checksumType: sha256
    checksum: "abc123def456"
    mode: whole-disk
  disk:
    minSizeGB: 100
    numVFs: 32
    raid:
      - name: /dev/md0
        level: 1
        devices:
          - /dev/sda
          - /dev/sdb
  firmware:
    enabled: true
    url: "https://caprf.example.com/firmware"
    minBIOS: "2.10"
    minBMC: "4.50"
  cloudInit:
    enabled: true
    datasource: nocloud
  crashArtifacts:
    enabled: true
    prepareURL: "https://caprf.example.com/crash/prepare"
    uploadURL: "https://caprf.example.com/crash/upload"
    maxMB: 256
    uploadTimeoutSec: 120
  inventory:
    enabled: true
    url: "https://caprf.example.com/inventory"

agent:
  heartbeatURL: "https://caprf.example.com/heartbeat"
  commandsURL: "https://caprf.example.com/commands"
`
	path := writeTestFile(t, "config.yaml", content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Hostname != "test-node-01" {
		t.Errorf("Hostname = %q", cfg.Hostname)
	}
	if cfg.Mode != "provision" {
		t.Errorf("Mode = %q", cfg.Mode)
	}
	if cfg.Network.Mode != "gobgp" {
		t.Errorf("Network.Mode = %q", cfg.Network.Mode)
	}
	if cfg.Network.BGP.ASN != 65001 {
		t.Errorf("Network.BGP.ASN = %d", cfg.Network.BGP.ASN)
	}
	if cfg.Network.BGP.MinPeers != 2 {
		t.Errorf("Network.BGP.MinPeers = %d", cfg.Network.BGP.MinPeers)
	}
	if cfg.Network.EVPN.ProvisionVNI != 100 {
		t.Errorf("Network.EVPN.ProvisionVNI = %d", cfg.Network.EVPN.ProvisionVNI)
	}
	if cfg.Network.Bond.Interfaces != "eth0,eth1" {
		t.Errorf("Network.Bond.Interfaces = %q", cfg.Network.Bond.Interfaces)
	}
	if cfg.Network.VRF.TableID != 1000 {
		t.Errorf("Network.VRF.TableID = %d", cfg.Network.VRF.TableID)
	}
	if cfg.Transport.Token != "bootstrap-token-xyz" {
		t.Errorf("Transport.Token = %q", cfg.Transport.Token)
	}
	if cfg.Transport.TokenAlgorithm != "RS256" {
		t.Errorf("Transport.TokenAlgorithm = %q", cfg.Transport.TokenAlgorithm)
	}
	if cfg.Health.Enabled != true {
		t.Error("Health.Enabled = false")
	}
	if cfg.Health.MinMemoryGB != 16 {
		t.Errorf("Health.MinMemoryGB = %d", cfg.Health.MinMemoryGB)
	}
	if cfg.Telemetry.Enabled != true {
		t.Error("Telemetry.Enabled = false")
	}
	if cfg.Rescue.Mode != "retry" {
		t.Errorf("Rescue.Mode = %q", cfg.Rescue.Mode)
	}
	if cfg.Rescue.Timeout != 300 {
		t.Errorf("Rescue.Timeout = %d", cfg.Rescue.Timeout)
	}
	if cfg.Provision.ExtraKernelParams != "console=ttyS0,115200" {
		t.Errorf("Provision.ExtraKernelParams = %q", cfg.Provision.ExtraKernelParams)
	}
	if cfg.Provision.FailureDomain != "zone-a" {
		t.Errorf("Provision.FailureDomain = %q", cfg.Provision.FailureDomain)
	}
	if len(cfg.Provision.Image.URLs) != 1 {
		t.Fatalf("Provision.Image.URLs len = %d", len(cfg.Provision.Image.URLs))
	}
	if cfg.Provision.Image.ChecksumType != "sha256" {
		t.Errorf("Provision.Image.ChecksumType = %q", cfg.Provision.Image.ChecksumType)
	}
	if cfg.Provision.Disk.MinSizeGB != 100 {
		t.Errorf("Provision.Disk.MinSizeGB = %d", cfg.Provision.Disk.MinSizeGB)
	}
	if len(cfg.Provision.Disk.RAID) != 1 {
		t.Fatalf("Provision.Disk.RAID len = %d", len(cfg.Provision.Disk.RAID))
	}
	if cfg.Provision.Disk.RAID[0].Level != 1 {
		t.Errorf("RAID[0].Level = %d", cfg.Provision.Disk.RAID[0].Level)
	}
	if len(cfg.Provision.Disk.RAID[0].Devices) != 2 {
		t.Errorf("RAID[0].Devices len = %d", len(cfg.Provision.Disk.RAID[0].Devices))
	}
	if len(cfg.Provision.PostProvisionCmds) != 2 {
		t.Errorf("PostProvisionCmds len = %d", len(cfg.Provision.PostProvisionCmds))
	}
	if cfg.Provision.Firmware.MinBIOS != "2.10" {
		t.Errorf("Provision.Firmware.MinBIOS = %q", cfg.Provision.Firmware.MinBIOS)
	}
	if cfg.Provision.CrashArtifacts.MaxMB != 256 {
		t.Errorf("Provision.CrashArtifacts.MaxMB = %d", cfg.Provision.CrashArtifacts.MaxMB)
	}
	if cfg.Agent.HeartbeatURL != "https://caprf.example.com/heartbeat" {
		t.Errorf("Agent.HeartbeatURL = %q", cfg.Agent.HeartbeatURL)
	}
}

func TestLoadJSON(t *testing.T) {
	content := `{
  "hostname": "json-node",
  "mode": "provision",
  "network": {
    "mode": "static",
    "static": {"ip": "10.0.0.5/24", "gateway": "10.0.0.1"}
  },
  "transport": {"token": "json-token", "initURL": "https://example.com/init"},
  "provision": {
    "image": {"urls": ["https://example.com/image.gz"], "mode": "whole-disk"},
    "disk": {"minSizeGB": 50}
  }
}`
	path := writeTestFile(t, "config.json", content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Hostname != "json-node" {
		t.Errorf("Hostname = %q", cfg.Hostname)
	}
	if cfg.Network.Static.IP != "10.0.0.5/24" {
		t.Errorf("Network.Static.IP = %q", cfg.Network.Static.IP)
	}
	if cfg.Provision.Disk.MinSizeGB != 50 {
		t.Errorf("Provision.Disk.MinSizeGB = %d", cfg.Provision.Disk.MinSizeGB)
	}
}

func TestLoadStrictYAMLRejectsUnknownFields(t *testing.T) {
	content := `
hostname: test
unknownField: bad
`
	path := writeTestFile(t, "strict.yaml", content)
	_, err := LoadWithOptions(LoadOptions{Path: path, Strict: true})
	if err == nil {
		t.Fatal("expected error for unknown field in strict mode")
	}
	if !strings.Contains(err.Error(), "unknownField") {
		t.Fatalf("error should mention unknown field: %v", err)
	}
}

func TestLoadStrictJSONRejectsUnknownFields(t *testing.T) {
	content := `{"hostname":"test","badField":true}`
	path := writeTestFile(t, "strict.json", content)
	_, err := LoadWithOptions(LoadOptions{Path: path, Strict: true})
	if err == nil {
		t.Fatal("expected error for unknown field in strict mode")
	}
}

func TestLoadValidationRuns(t *testing.T) {
	content := `
hostname: test
mode: totally-invalid-mode
`
	path := writeTestFile(t, "invalid.yaml", content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("expected 'invalid mode' in error: %v", err)
	}
}

func TestLoadUnsupportedExtension(t *testing.T) {
	path := writeTestFile(t, "config.toml", "hostname = test")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
	if !strings.Contains(err.Error(), "unsupported config format") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoadEmptyPath(t *testing.T) {
	_, err := Load("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestLoadPartialYAML(t *testing.T) {
	content := `
hostname: partial-node
provision:
  image:
    urls:
      - "https://example.com/image.gz"
`
	path := writeTestFile(t, "partial.yaml", content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Hostname != "partial-node" {
		t.Errorf("Hostname = %q", cfg.Hostname)
	}
	if cfg.Mode != "" {
		t.Errorf("Mode should be empty, got %q", cfg.Mode)
	}
	if len(cfg.Provision.Image.URLs) != 1 {
		t.Errorf("Image.URLs len = %d", len(cfg.Provision.Image.URLs))
	}
}

func TestLoadJSONTrailingContent(t *testing.T) {
	content := `{"hostname":"test"}{"extra":true}`
	path := writeTestFile(t, "trailing.json", content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for trailing JSON content")
	}
}

func TestLoadYMLExtension(t *testing.T) {
	content := `hostname: yml-node`
	path := writeTestFile(t, "config.yml", content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Hostname != "yml-node" {
		t.Errorf("Hostname = %q", cfg.Hostname)
	}
}

func writeTestFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
