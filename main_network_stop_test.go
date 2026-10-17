//go:build linux

package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/network"
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

func TestWithNetworkStopBeforeReboot(t *testing.T) {
	var order []string
	ops := provisionHandoffOps{
		reboot:   func() error { order = append(order, "reboot"); return errors.New("x") },
		powerOff: func() error { order = append(order, "poweroff"); return nil },
	}
	ops = withNetworkStopBeforeReboot(ops, func() { order = append(order, "stop") })
	_ = ops.powerOff()
	if err := ops.reboot(); err == nil {
		t.Fatal("reboot error lost")
	}
	if got := len(order); got != 3 || order[0] != "poweroff" || order[1] != "stop" || order[2] != "reboot" {
		t.Fatalf("order = %v, want poweroff without stop, then stop before reboot", order)
	}
}
