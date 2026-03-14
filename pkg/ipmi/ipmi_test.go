//go:build linux

package ipmi

import (
	"testing"
)

func TestNew(t *testing.T) {
	t.Helper()
	m := New(nil)
	if m.device != "/dev/ipmi0" {
		t.Errorf("device = %q, want /dev/ipmi0", m.device)
	}
}

func TestDevicePath(t *testing.T) {
	m := &Manager{device: "/dev/ipmi0"}
	if got := m.DevicePath(); got != "/dev/ipmi0" {
		t.Errorf("DevicePath = %q, want /dev/ipmi0", got)
	}
}

func TestBootDeviceConstants(t *testing.T) {
	tests := []struct {
		device   BootDevice
		expected string
	}{
		{BootPXE, "pxe"},
		{BootDisk, "disk"},
		{BootCDROM, "cdrom"},
		{BootBIOS, "bios"},
	}

	for _, tc := range tests {
		if string(tc.device) != tc.expected {
			t.Errorf("BootDevice %q != %q", tc.device, tc.expected)
		}
	}
}

func TestValidateBootDevice(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"pxe", false},
		{"disk", false},
		{"cdrom", false},
		{"bios", false},
		{"invalid", true},
		{"", true},
	}

	for _, tc := range tests {
		err := ValidateBootDevice(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ValidateBootDevice(%q) should error", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("ValidateBootDevice(%q) unexpected error: %v", tc.input, err)
			}
		}
	}
}

func TestParseBMCMAC(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"aa:bb:cc:dd:ee:ff", false},
		{"AA:BB:CC:DD:EE:FF", false},
		{"invalid", true},
		{"", true},
	}

	for _, tc := range tests {
		hw, err := ParseBMCMAC(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseBMCMAC(%q) should error", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseBMCMAC(%q) unexpected error: %v", tc.input, err)
			}
			if hw == nil {
				t.Errorf("ParseBMCMAC(%q) returned nil", tc.input)
			}
		}
	}
}

func TestChassisStatus_Fields(t *testing.T) {
	status := ChassisStatus{
		PowerOn:    true,
		PowerFault: false,
		Intrusion:  false,
		LastEvent:  "power-on",
	}

	if !status.PowerOn {
		t.Error("PowerOn should be true")
	}
	if status.PowerFault {
		t.Error("PowerFault should be false")
	}
}

func TestBMCNetConfig_Fields(t *testing.T) {
	cfg := BMCNetConfig{
		IPAddress:   "10.0.0.100",
		Netmask:     "255.255.255.0",
		Gateway:     "10.0.0.1",
		MACAddress:  "aa:bb:cc:dd:ee:ff",
		DHCP:        false,
		VLANEnabled: true,
		VLANID:      100,
	}

	if cfg.DHCP {
		t.Error("DHCP should be false")
	}
	if !cfg.VLANEnabled {
		t.Error("VLANEnabled should be true")
	}
	if cfg.VLANID != 100 {
		t.Errorf("VLANID = %d, want 100", cfg.VLANID)
	}
}

func TestSensorReading_Fields(t *testing.T) {
	reading := SensorReading{
		Name:      "CPU Temp",
		Value:     45.0,
		Unit:      "C",
		Status:    "ok",
		LowerCrit: 5.0,
		UpperCrit: 95.0,
	}

	if reading.Value != 45.0 {
		t.Errorf("Value = %f, want 45.0", reading.Value)
	}
	if reading.Status != "ok" {
		t.Errorf("Status = %q, want ok", reading.Status)
	}
}

func TestParseLanPrint(t *testing.T) {
	output := `Set in Progress         : Set Complete
IP Address Source       : DHCP Address
IP Address              : 10.0.0.100
Subnet Mask             : 255.255.255.0
MAC Address             : aa:bb:cc:dd:ee:ff
Default Gateway IP      : 10.0.0.1
`

	fields := parseLanPrint(output)
	if fields["IP Address"] != "10.0.0.100" {
		t.Errorf("IP Address = %q", fields["IP Address"])
	}
	if fields["IP Address Source"] != "DHCP Address" {
		t.Errorf("IP Address Source = %q", fields["IP Address Source"])
	}
	if fields["MAC Address"] != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MAC Address = %q", fields["MAC Address"])
	}
}
