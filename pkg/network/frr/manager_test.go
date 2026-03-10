//go:build linux

package frr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// mockCommander records calls and returns preset results.
type mockFRRCommander struct {
	calls   []string
	results map[string]mockFRRResult
}

type mockFRRResult struct {
	output []byte
	err    error
}

func newMockFRRCommander() *mockFRRCommander {
	return &mockFRRCommander{results: make(map[string]mockFRRResult)}
}

func (m *mockFRRCommander) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name
	if len(args) > 0 {
		key = name + " " + args[0]
	}
	m.calls = append(m.calls, key)
	if r, ok := m.results[key]; ok {
		return r.output, r.err
	}
	return nil, nil
}

func (m *mockFRRCommander) setResult(key string, output []byte, err error) {
	m.results[key] = mockFRRResult{output: output, err: err}
}

func TestNewManager(t *testing.T) {
	mgr := NewManager(nil)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
}

func TestNewManagerCustomCommander(t *testing.T) {
	cmd := newMockFRRCommander()
	mgr := NewManager(cmd)
	if mgr.commander != cmd {
		t.Fatal("expected custom commander")
	}
}

func TestTeardown(t *testing.T) {
	cmd := newMockFRRCommander()
	mgr := NewManager(cmd)

	if err := mgr.Teardown(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(cmd.calls))
	}
}

func TestWaitForConnectivitySuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cmd := newMockFRRCommander()
	mgr := NewManager(cmd)

	if err := mgr.WaitForConnectivity(context.Background(), srv.URL, 5*time.Second); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForConnectivityTimeout(t *testing.T) {
	// Server that always fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	srv.Close() // close immediately so connections fail

	cmd := newMockFRRCommander()
	mgr := NewManager(cmd)

	err := mgr.WaitForConnectivity(context.Background(), srv.URL, 2*time.Second)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestWaitForConnectivityEmptyTarget(t *testing.T) {
	cmd := newMockFRRCommander()
	mgr := NewManager(cmd)

	err := mgr.WaitForConnectivity(context.Background(), "", 1*time.Second)
	if err == nil {
		t.Fatal("expected error for empty target")
	}
}

func TestWaitForConnectivityContextCancel(t *testing.T) {
	// Server always fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second) // slow response
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cmd := newMockFRRCommander()
	mgr := NewManager(cmd)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := mgr.WaitForConnectivity(ctx, "http://192.0.2.1:9999/unreachable", 30*time.Second)
	if err == nil {
		t.Fatal("expected context cancel error")
	}
}

func TestAddBGPPeer(t *testing.T) {
	cmd := newMockFRRCommander()
	mgr := NewManager(cmd)

	err := mgr.addBGPPeer(context.Background(), "Vrf_underlay", 65000, "eth0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(cmd.calls))
	}
}

func TestAddBGPPeerError(t *testing.T) {
	cmd := newMockFRRCommander()
	cmd.setResult("vtysh -c", nil, fmt.Errorf("vtysh failed"))
	mgr := NewManager(cmd)

	err := mgr.addBGPPeer(context.Background(), "Vrf_underlay", 65000, "eth0")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStartFRRSystemctl(t *testing.T) {
	cmd := newMockFRRCommander()
	mgr := NewManager(cmd)

	if err := mgr.startFRR(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(cmd.calls))
	}
}

func TestStartFRRFallback(t *testing.T) {
	cmd := newMockFRRCommander()
	cmd.setResult("systemctl restart", nil, fmt.Errorf("systemctl failed"))
	mgr := NewManager(cmd)

	// startDaemonsDirect will try to stat daemon paths; won't find them in test.
	if err := mgr.startFRR(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForHTTPWithFRRRestartsOnFailure(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := callCount.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cmd := newMockFRRCommander()
	// The function should succeed after a few retries.
	err := waitForHTTPWithFRR(context.Background(), cmd, srv.URL, 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
