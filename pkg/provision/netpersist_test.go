package provision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectOSFamily(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{"ubuntu", "ID=ubuntu\nVERSION_ID=\"22.04\"", "ubuntu"},
		{"flatcar", "ID=flatcar\nVERSION_ID=3510.2.1", "flatcar"},
		{"rhel", "ID=\"rhel\"\nVERSION_ID=\"8.8\"", "rhel"},
		{"rocky", "ID=\"rocky\"\nVERSION_ID=\"9.2\"", "rhel"},
		{"centos", "ID=\"centos\"\nVERSION_ID=\"7\"", "rhel"},
		{"unknown", "ID=arch", "ubuntu"},
		{"missing", "", "ubuntu"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.content != "" {
				if err := os.MkdirAll(filepath.Join(dir, "etc"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "etc", "os-release"), []byte(tt.content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			got := detectOSFamily(dir)
			if got != tt.expected {
				t.Errorf("detectOSFamily() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestWriteNetplanStatic(t *testing.T) {
	dir := t.TempDir()
	err := writeNetplan(dir, "10.0.1.5/24", "10.0.1.1", "8.8.8.8,8.8.4.4")
	if err != nil {
		t.Fatalf("writeNetplan() error = %v", err)
	}

	path := filepath.Join(dir, "etc", "netplan", "01-booty-provisioned.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read netplan file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "10.0.1.5/24") {
		t.Error("netplan missing static IP address")
	}
	if !strings.Contains(content, "10.0.1.1") {
		t.Error("netplan missing gateway")
	}
	if !strings.Contains(content, "8.8.8.8") {
		t.Error("netplan missing DNS")
	}
}

func TestWriteNetplanDHCP(t *testing.T) {
	dir := t.TempDir()
	err := writeNetplan(dir, "", "", "")
	if err != nil {
		t.Fatalf("writeNetplan() error = %v", err)
	}

	path := filepath.Join(dir, "etc", "netplan", "01-booty-provisioned.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read netplan file: %v", err)
	}

	if !strings.Contains(string(data), "dhcp4: true") {
		t.Error("netplan missing dhcp4")
	}
}

func TestWriteSystemdNetworkdStatic(t *testing.T) {
	dir := t.TempDir()
	err := writeSystemdNetworkd(dir, "10.0.1.5/24", "10.0.1.1", "8.8.8.8,8.8.4.4")
	if err != nil {
		t.Fatalf("writeSystemdNetworkd() error = %v", err)
	}

	path := filepath.Join(dir, "etc", "systemd", "network", "10-booty.network")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read systemd-networkd file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "Address=10.0.1.5/24") {
		t.Error("networkd missing static address")
	}
	if !strings.Contains(content, "Gateway=10.0.1.1") {
		t.Error("networkd missing gateway")
	}
	if !strings.Contains(content, "DNS=8.8.8.8") {
		t.Error("networkd missing DNS")
	}
}

func TestWriteSystemdNetworkdDHCP(t *testing.T) {
	dir := t.TempDir()
	err := writeSystemdNetworkd(dir, "", "", "")
	if err != nil {
		t.Fatalf("writeSystemdNetworkd() error = %v", err)
	}

	path := filepath.Join(dir, "etc", "systemd", "network", "10-booty.network")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read systemd-networkd file: %v", err)
	}

	if !strings.Contains(string(data), "DHCP=yes") {
		t.Error("networkd missing DHCP")
	}
}
