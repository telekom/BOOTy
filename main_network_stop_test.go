//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/network"
	"github.com/telekom/BOOTy/pkg/runmode"
)

type teardownMode struct {
	network.Mode
	calls atomic.Int32
	block chan struct{}
}

func (m *teardownMode) Teardown(context.Context) error {
	m.calls.Add(1)
	if m.block != nil {
		<-m.block
	}
	return nil
}

func TestStopNetworkOnceTearsDownOnce(t *testing.T) {
	mode := &teardownMode{}
	stop := stopNetworkOnce(context.Background(), mode)
	stop()
	stop()
	if got := mode.calls.Load(); got != 1 {
		t.Fatalf("Teardown calls = %d, want 1", got)
	}
	stopNetworkOnce(context.Background(), nil)() // no network mode: no panic
}

// A hung teardown must not stop the handoff.
func TestStopNetworkOnceBoundsTheWait(t *testing.T) {
	defer func(d time.Duration) { networkStopTimeout = d }(networkStopTimeout)
	networkStopTimeout = 50 * time.Millisecond
	mode := &teardownMode{block: make(chan struct{})}
	defer close(mode.block)

	start := time.Now()
	stopNetworkOnce(context.Background(), mode)()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("stop waited %v, want about %v", d, networkStopTimeout)
	}
}

// recordOrder replaces flushLogs and kexecExecute and records the calls.
func recordOrder(t *testing.T, order *[]string) {
	t.Helper()
	prevFlush, prevExec := flushLogs, kexecExecute
	t.Cleanup(func() { flushLogs, kexecExecute = prevFlush, prevExec })
	flushLogs = func() { *order = append(*order, "flush") }
	kexecExecute = func() error { *order = append(*order, "kexec"); return errors.New("test: no kexec") }
}

// The log lines are shipped while the network is up, then BGP and BFD
// stop, then the kexec runs.
func TestDoKexecExecuteFlushesThenStopsNetwork(t *testing.T) {
	var order []string
	recordOrder(t, &order)
	if err := doKexecExecute(func() { order = append(order, "stop") }); err == nil {
		t.Fatal("kexec error lost")
	}
	if strings.Join(order, ",") != "flush,stop,kexec" {
		t.Fatalf("order = %v, want flush,stop,kexec", order)
	}
}

// The wiring of tryKexec: after the load, the network stops between the log
// flush and the kexec.
func TestTryKexecStopsNetworkBeforeExecute(t *testing.T) {
	var order []string
	recordOrder(t, &order)
	root := t.TempDir()
	for _, f := range []string{"boot/vmlinuz", "boot/initrd.img"} {
		mustWrite(t, filepath.Join(root, f))
	}
	if err := os.MkdirAll(filepath.Join(root, "boot", "grub"), 0o755); err != nil {
		t.Fatal(err)
	}
	grub := "menuentry 'os' {\n linux /boot/vmlinuz root=/dev/sda2\n initrd /boot/initrd.img\n}\n"
	if err := os.WriteFile(filepath.Join(root, "boot", "grub", "grub.cfg"), []byte(grub), 0o644); err != nil {
		t.Fatal(err)
	}
	prevRoot, prevLoad := kexecRoot, kexecLoad
	t.Cleanup(func() { kexecRoot, kexecLoad = prevRoot, prevLoad })
	kexecRoot = root
	kexecLoad = func(string, string, string) error { order = append(order, "load"); return nil }
	keep := false
	cfg := &config.MachineConfig{}
	cfg.Provision.KexecUnmountTarget = &keep

	if tryKexec(cfg, false, nil, func() { order = append(order, "stop") }) {
		t.Fatal("tryKexec reported a kexec although execute failed")
	}
	if strings.Join(order, ",") != "load,flush,stop,kexec" {
		t.Fatalf("order = %v, want load,flush,stop,kexec", order)
	}
}

func TestWithNetworkStopBeforeReboot(t *testing.T) {
	var order []string
	recordOrder(t, &order)
	ops := provisionHandoffOps{
		reboot:   func() error { order = append(order, "reboot"); return errors.New("x") },
		powerOff: func() error { order = append(order, "poweroff"); return nil },
	}
	ops = withNetworkStopBeforeReboot(ops, func() { order = append(order, "stop") })
	_ = ops.powerOff()
	if err := ops.reboot(); err == nil {
		t.Fatal("reboot error lost")
	}
	if strings.Join(order, ",") != "poweroff,flush,stop,reboot" {
		t.Fatalf("order = %v, want poweroff without stop, then flush, stop, reboot", order)
	}
}

// The wiring of runCAPRF: a provision that reboots stops the network once,
// after the log flush and before the reboot.
func TestFinishProvisionStopsNetworkBeforeReboot(t *testing.T) {
	var order []string
	recordOrder(t, &order)
	prevOps := provisionHandoff
	t.Cleanup(func() { provisionHandoff = prevOps })
	provisionHandoff = provisionHandoffOps{
		reboot:   func() error { order = append(order, "reboot"); return nil },
		powerOff: func() error { order = append(order, "poweroff"); return nil },
		shell:    func() {},
		exit:     func(int) {},
	}
	mode := &orderMode{order: &order}
	cfg := &config.MachineConfig{}
	cfg.Provision.DisableKexec = true
	finishProvision(context.Background(), cfg, nil, nil, mode, &runmode.ProvisionCompleteError{})
	if strings.Join(order, ",") != "flush,teardown,reboot" {
		t.Fatalf("order = %v, want flush,teardown,reboot", order)
	}
}

type orderMode struct {
	network.Mode
	order *[]string
}

func (m *orderMode) Teardown(context.Context) error {
	*m.order = append(*m.order, "teardown")
	return nil
}
