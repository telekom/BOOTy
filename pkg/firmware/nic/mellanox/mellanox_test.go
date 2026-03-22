//go:build linux

package mellanox

import (
	"context"
	"fmt"
	"testing"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
)

func TestSupported(t *testing.T) {
	m := New(nil)
	if !m.Supported(&nic.Identifier{VendorID: "0x15b3"}) {
		t.Error("should support Mellanox NIC")
	}
	if m.Supported(&nic.Identifier{VendorID: "0x8086"}) {
		t.Error("should not support Intel NIC")
	}
}

func TestVendor(t *testing.T) {
	m := New(nil)
	if m.Vendor() != nic.VendorMellanox {
		t.Errorf("vendor = %q", m.Vendor())
	}
}

func TestParseMstconfigLine(t *testing.T) {
	tests := []struct {
		line    string
		wantOK  bool
		name    string
		current string
	}{
		{"SRIOV_EN                    True(True)", true, "SRIOV_EN", "True"},
		{"NUM_OF_VFS                  16(0)", true, "NUM_OF_VFS", "16"},
		{"LINK_TYPE_P1                ETH(ETH)", true, "LINK_TYPE_P1", "ETH"},
		{"", false, "", ""},
		{"Device #1:", false, "", ""},
		{"Configurations:", false, "", ""},
		{"X", false, "", ""},
	}

	for _, tc := range tests {
		name, param, ok := parseMstconfigLine(tc.line)
		if ok != tc.wantOK {
			t.Errorf("parseMstconfigLine(%q) ok=%v, want %v", tc.line, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if name != tc.name {
			t.Errorf("name = %q, want %q", name, tc.name)
		}
		if param.Current != tc.current {
			t.Errorf("current = %q, want %q", param.Current, tc.current)
		}
	}
}

func TestCriticalParams(t *testing.T) {
	params := CriticalParams()
	if len(params) == 0 {
		t.Error("CriticalParams should not be empty")
	}
	found := false
	for _, p := range params {
		if p == "SRIOV_EN" {
			found = true
		}
	}
	if !found {
		t.Error("CriticalParams should contain SRIOV_EN")
	}
}

// MockCommander returns pre-canned outputs for testing.
type MockCommander struct {
	queryOut string
	setOut   string
	err      error
	called   []string
}

func (m *MockCommander) CombinedOutput(_ context.Context, cmd string, args ...string) ([]byte, error) {
	m.called = append(m.called, cmd)
	if m.err != nil {
		return nil, m.err
	}
	for _, a := range args {
		if a == "set" {
			return []byte(m.setOut), nil
		}
	}
	return []byte(m.queryOut), nil
}

func TestCapture_Success(t *testing.T) {
	mock := &MockCommander{
		queryOut: "Device #1:\nConfigurations:\nSRIOV_EN                    True(True)\nNUM_OF_VFS                  16(0)\n",
	}
	m := NewWithCommander(nil, mock)

	id := &nic.Identifier{PCIAddress: "0000:03:00.0", VendorID: "0x15b3"}

	state, err := m.Capture(context.Background(), id)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if p, ok := state.Parameters["SRIOV_EN"]; !ok || p.Current != "True" {
		t.Error("SRIOV_EN not captured")
	}
	if p, ok := state.Parameters["NUM_OF_VFS"]; !ok || p.Current != "16" {
		t.Error("NUM_OF_VFS not captured")
	}
}

func TestCapture_NilNIC(t *testing.T) {
	m := New(nil)
	if _, err := m.Capture(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil NIC")
	}
}

func TestCapture_MstconfigError(t *testing.T) {
	mock := &MockCommander{err: fmt.Errorf("mstconfig not found")}
	m := NewWithCommander(nil, mock)

	id := &nic.Identifier{PCIAddress: "0000:03:00.0", VendorID: "0x15b3"}
	if _, err := m.Capture(context.Background(), id); err == nil {
		t.Fatal("expected error")
	}
}

func TestApply_Success(t *testing.T) {
	mock := &MockCommander{}
	m := NewWithCommander(nil, mock)

	id := &nic.Identifier{PCIAddress: "0000:03:00.0", VendorID: "0x15b3"}
	changes := []nic.FlagChange{{Name: "SRIOV_EN", Value: "True"}}

	if err := m.Apply(context.Background(), id, changes); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(mock.called) == 0 {
		t.Fatal("Apply did not invoke mstconfig")
	}
}

func TestApply_Error(t *testing.T) {
	mock := &MockCommander{err: fmt.Errorf("permission denied")}
	m := NewWithCommander(nil, mock)

	id := &nic.Identifier{PCIAddress: "0000:03:00.0", VendorID: "0x15b3"}
	changes := []nic.FlagChange{{Name: "SRIOV_EN", Value: "True"}}

	if err := m.Apply(context.Background(), id, changes); err == nil {
		t.Fatal("expected error")
	}
}

func TestApply_NilNIC(t *testing.T) {
	m := New(nil)
	if err := m.Apply(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error for nil NIC")
	}
}
