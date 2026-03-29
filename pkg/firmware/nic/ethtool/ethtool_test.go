//go:build linux

package ethtool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/firmware/nic"
)

type fakeCommander struct {
	outputs map[string][]byte
	errs    map[string]error
	calls   []string
}

func (f *fakeCommander) CombinedOutput(_ context.Context, cmd string, args ...string) ([]byte, error) {
	key := cmd + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)

	if err, ok := f.errs[key]; ok {
		return f.outputs[key], err
	}
	if out, ok := f.outputs[key]; ok {
		return out, nil
	}
	return nil, nil
}

func TestSupported(t *testing.T) {
	mgr := New(nic.VendorIntel, "0x8086", nil)
	if !mgr.Supported(&nic.Identifier{VendorID: "0x8086"}) {
		t.Fatal("Supported() = false, want true")
	}
	if mgr.Supported(&nic.Identifier{VendorID: "0x15b3"}) {
		t.Fatal("Supported() = true, want false")
	}
}

func TestCaptureParsesInfoAndPrivateFlags(t *testing.T) {
	fake := &fakeCommander{
		outputs: map[string][]byte{
			"ethtool -i eth0":                []byte("driver: ixgbe\nfirmware-version: 1.2.3\nbus-info: 0000:01:00.0\n"),
			"ethtool --show-priv-flags eth0": []byte("Private flags for eth0:\nflag_a: on\nflag_b: off\n"),
		},
		errs: map[string]error{},
	}

	mgr := NewWithCommander(nic.VendorIntel, "0x8086", nil, fake)
	state, err := mgr.Capture(context.Background(), &nic.Identifier{PCIAddress: "0000:01:00.0", Interface: "eth0", VendorID: "0x8086"})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}

	if state.FWVersion != "1.2.3" {
		t.Fatalf("FWVersion = %q, want %q", state.FWVersion, "1.2.3")
	}
	if got := state.Parameters["flag_a"].Current; got != "on" {
		t.Fatalf("flag_a = %q, want %q", got, "on")
	}
	if got := state.Parameters["flag_b"].Current; got != "off" {
		t.Fatalf("flag_b = %q, want %q", got, "off")
	}
}

func TestCaptureIgnoresPrivateFlagsFailure(t *testing.T) {
	fake := &fakeCommander{
		outputs: map[string][]byte{
			"ethtool -i eth0": []byte("driver: ixgbe\nfirmware-version: 1.2.3\n"),
		},
		errs: map[string]error{
			"ethtool --show-priv-flags eth0": errors.New("unsupported"),
		},
	}

	mgr := NewWithCommander(nic.VendorIntel, "0x8086", nil, fake)
	state, err := mgr.Capture(context.Background(), &nic.Identifier{Interface: "eth0", VendorID: "0x8086"})
	if err != nil {
		t.Fatalf("Capture() error: %v", err)
	}
	if state.FWVersion != "1.2.3" {
		t.Fatalf("FWVersion = %q, want %q", state.FWVersion, "1.2.3")
	}
}

func TestApplyInvokesSetPrivFlags(t *testing.T) {
	fake := &fakeCommander{outputs: map[string][]byte{}, errs: map[string]error{}}
	mgr := NewWithCommander(nic.VendorIntel, "0x8086", nil, fake)

	err := mgr.Apply(context.Background(), &nic.Identifier{Interface: "eth0", VendorID: "0x8086"}, []nic.FlagChange{{Name: "flag_a", Value: "on"}})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}

	want := "ethtool --set-priv-flags eth0 flag_a on"
	if len(fake.calls) == 0 || fake.calls[0] != want {
		t.Fatalf("calls = %v, want first call %q", fake.calls, want)
	}
}

func TestCaptureValidatesInputs(t *testing.T) {
	mgr := New(nic.VendorIntel, "0x8086", nil)
	if _, err := mgr.Capture(context.Background(), nil); err == nil {
		t.Fatal("Capture(nil) error = nil, want non-nil")
	}

	_, err := mgr.Capture(context.Background(), &nic.Identifier{VendorID: "0x8086"})
	if err == nil {
		t.Fatal("Capture() error = nil, want missing interface error")
	}
	if !strings.Contains(err.Error(), "interface name required") {
		t.Fatalf("Capture() error = %q, want interface name error", err.Error())
	}
}

func TestApplyPropagatesCommandFailure(t *testing.T) {
	fake := &fakeCommander{
		outputs: map[string][]byte{
			"ethtool --set-priv-flags eth0 flag_a on": []byte("permission denied"),
		},
		errs: map[string]error{
			"ethtool --set-priv-flags eth0 flag_a on": errors.New("exit status 1"),
		},
	}
	mgr := NewWithCommander(nic.VendorIntel, "0x8086", nil, fake)

	err := mgr.Apply(context.Background(), &nic.Identifier{Interface: "eth0", VendorID: "0x8086"}, []nic.FlagChange{{Name: "flag_a", Value: "on"}})
	if err == nil {
		t.Fatal("Apply() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "set flag_a=on") {
		t.Fatalf("Apply() error = %q, want wrapped flag context", err.Error())
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("Apply() error = %q, want command output", err.Error())
	}
}

func TestNewWithCommanderDefaults(t *testing.T) {
	mgr := NewWithCommander(nic.VendorBroadcom, "0x14e4", nil, nil)
	if mgr.log == nil {
		t.Fatal("manager logger is nil")
	}

	_, ok := mgr.commander.(OSCommander)
	if !ok {
		t.Fatalf("default commander type = %T, want OSCommander", mgr.commander)
	}
}

func TestApplyViaEthtoolFormatsError(t *testing.T) {
	fake := &fakeCommander{
		outputs: map[string][]byte{
			"ethtool --set-priv-flags eth0 foo bar": []byte("bad flag"),
		},
		errs: map[string]error{
			"ethtool --set-priv-flags eth0 foo bar": errors.New("exit status 2"),
		},
	}
	mgr := NewWithCommander(nic.VendorIntel, "0x8086", nil, fake)

	err := mgr.ApplyViaEthtool(context.Background(), "eth0", nic.FlagChange{Name: "foo", Value: "bar"})
	if err == nil {
		t.Fatal("ApplyViaEthtool() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "ethtool set-priv-flags") {
		t.Fatalf("ApplyViaEthtool() error = %q, want command context", err.Error())
	}
}

func ExampleManager_Supported() {
	mgr := New(nic.VendorIntel, "0x8086", nil)
	fmt.Println(mgr.Supported(&nic.Identifier{VendorID: "0x8086"}))
	// Output: true
}
