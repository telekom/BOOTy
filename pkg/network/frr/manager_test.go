//go:build linux

package frr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestEnsureFRRDirsCreatesRuntimeDirsWithFRROwnership(t *testing.T) {
	base := t.TempDir()
	dirs := []string{
		filepath.Join(base, "run", "frr"),
		filepath.Join(base, "var", "run", "frr"),
		filepath.Join(base, "var", "tmp", "frr"),
		filepath.Join(base, "var", "lib", "frr"),
	}

	origDirs := frrRuntimeDirs
	origLookup := lookupFRRUser
	origChown := chownFRRDir
	frrRuntimeDirs = dirs
	lookupFRRUser = func() frrDirOwner {
		return frrDirOwner{uid: 101, gid: 104, ok: true}
	}

	chowned := make(map[string]bool, len(dirs))
	chownFRRDir = func(path string, uid, gid int) error {
		if uid != 101 || gid != 104 {
			t.Fatalf("chown(%q) owner = %d:%d, want 101:104", path, uid, gid)
		}
		chowned[path] = true
		return nil
	}
	t.Cleanup(func() {
		frrRuntimeDirs = origDirs
		lookupFRRUser = origLookup
		chownFRRDir = origChown
	})

	ensureFRRDirs()

	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("expected FRR dir %q: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected FRR path %q to be a directory", dir)
		}
		if !chowned[dir] {
			t.Fatalf("expected FRR dir %q to be chowned", dir)
		}
	}
}

func TestEnsureFRRDirsSkipsOwnershipWhenFRRUserMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run", "frr")

	origDirs := frrRuntimeDirs
	origLookup := lookupFRRUser
	origChown := chownFRRDir
	frrRuntimeDirs = []string{dir}
	lookupFRRUser = func() frrDirOwner { return frrDirOwner{} }
	chownFRRDir = func(path string, uid, gid int) error {
		t.Fatalf("unexpected chown(%q, %d, %d)", path, uid, gid)
		return nil
	}
	t.Cleanup(func() {
		frrRuntimeDirs = origDirs
		lookupFRRUser = origLookup
		chownFRRDir = origChown
	})

	ensureFRRDirs()

	if info, err := os.Stat(dir); err != nil {
		t.Fatalf("expected FRR dir %q: %v", dir, err)
	} else if !info.IsDir() {
		t.Fatalf("expected FRR path %q to be a directory", dir)
	}
}

func TestStartDaemonsDirectFailsWhenRequiredDaemonsMissing(t *testing.T) {
	origDirs := frrDaemonDirs
	frrDaemonDirs = []string{t.TempDir(), t.TempDir()}
	t.Cleanup(func() { frrDaemonDirs = origDirs })

	mgr := NewManager(nil)
	err := mgr.startDaemonsDirect(context.Background())
	if err == nil {
		t.Fatal("expected error for missing required FRR daemons")
	}
	if !strings.Contains(err.Error(), "required FRR daemons not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveFRRDaemonsSupportsInitramfsSbinLayout(t *testing.T) {
	sbin := t.TempDir()
	usrLib := t.TempDir()
	origDirs := frrDaemonDirs
	frrDaemonDirs = []string{usrLib, sbin}
	t.Cleanup(func() { frrDaemonDirs = origDirs })

	for _, name := range []string{"zebra", "bgpd", "bfdd"} {
		path := filepath.Join(sbin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write fake daemon %s: %v", name, err)
		}
	}

	paths, missing, err := resolveFRRDaemons([]frrDaemonSpec{
		{"mgmtd", nil, false},
		{"zebra", nil, true},
		{"bgpd", nil, true},
		{"bfdd", nil, true},
	})
	if err != nil {
		t.Fatalf("resolve daemons: %v", err)
	}
	if len(missing) > 0 {
		t.Fatalf("unexpected missing daemons: %v", missing)
	}
	for _, name := range []string{"zebra", "bgpd", "bfdd"} {
		if got, want := paths[name], filepath.Join(sbin, name); got != want {
			t.Fatalf("%s path = %q, want %q", name, got, want)
		}
	}
}

func TestResolveFRRDaemonsSurfacesStatErrors(t *testing.T) {
	badDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write bad daemon dir: %v", err)
	}

	origDirs := frrDaemonDirs
	frrDaemonDirs = []string{badDir}
	t.Cleanup(func() { frrDaemonDirs = origDirs })

	_, _, err := resolveFRRDaemons([]frrDaemonSpec{
		{"zebra", nil, true},
	})
	if err == nil {
		t.Fatal("expected stat error")
	}
	if !strings.Contains(err.Error(), "stat FRR daemon") {
		t.Fatalf("error = %q, want stat FRR daemon context", err.Error())
	}
	if strings.Contains(err.Error(), "required FRR daemons not found") {
		t.Fatalf("error flattened stat failure into missing daemon: %v", err)
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

	// The function should succeed after a few retries.
	err := waitForHTTPWithFRR(context.Background(), srv.URL, 30*time.Second, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
