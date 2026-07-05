//go:build linux

package gobgp

import (
	"context"
	"errors"
	"testing"
)

func TestWaitForNICsContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	u := NewUnderlayTier(&Config{})
	_, err := u.waitForNICs(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForNICs error = %v, want context.Canceled", err)
	}
}

func TestWaitForConfiguredNICsContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	u := NewUnderlayTier(&Config{Interfaces: []string{"eth-test0"}})
	_, err := u.waitForConfiguredNICs(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForConfiguredNICs error = %v, want context.Canceled", err)
	}
}

func TestDiscoverLinkLocalPeerContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := discoverLinkLocalPeer(ctx, "eth-test0")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("discoverLinkLocalPeer error = %v, want context.Canceled", err)
	}
}
