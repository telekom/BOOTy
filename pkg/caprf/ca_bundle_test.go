package caprf

import (
	"bytes"
	"context"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/trust"
)

// CA_BUNDLE_FILE makes the callbacks trust a private CA without
// SKIP_TLS_VERIFY; an invalid bundle stops New before the first request.
func TestNewCABundleFile(t *testing.T) {
	t.Cleanup(trust.Reset)
	var calls int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	dir := t.TempDir()
	good := filepath.Join(dir, "ca-bundle.pem")
	if err := os.WriteFile(good, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	vars := func(bundle string) string {
		return "TOKEN=t\nINIT_URL=" + srv.URL + "/status/init\nSKIP_TLS_VERIFY=false\nCA_BUNDLE_FILE=" + bundle + "\n"
	}

	trust.Reset()
	for _, b := range []string{bad, filepath.Join(dir, "missing.pem")} {
		if _, err := New(writeVarsFile(t, vars(b))); err == nil || !strings.Contains(err.Error(), "CA_BUNDLE_FILE") {
			t.Fatalf("New() with bundle %s = %v, want CA_BUNDLE_FILE error", b, err)
		}
	}
	if trust.Roots() != nil {
		t.Fatal("an invalid bundle changed the roots")
	}

	c, err := New(writeVarsFile(t, vars(good)))
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if c.cfg.Transport.CABundleFile != good {
		t.Fatalf("CABundleFile = %q, want %q", c.cfg.Transport.CABundleFile, good)
	}
	if err := c.ReportStatus(context.Background(), config.StatusInit, ""); err != nil {
		t.Fatalf("ReportStatus() with the CA bundle = %v", err)
	}
	if calls != 1 {
		t.Fatalf("server got %d requests, want 1", calls)
	}
}

// SKIP_TLS_VERIFY=true makes CA_BUNDLE_FILE useless for the clients that
// skip the check: warnTransport says so once.
func TestWarnTransportCABundleWithSkipTLSVerify(t *testing.T) {
	for _, tt := range []struct {
		skip   bool
		bundle string
		want   int
	}{
		{skip: true, bundle: "/ca.pem", want: 1},
		{skip: true, bundle: "", want: 0},
		{skip: false, bundle: "/ca.pem", want: 0},
	} {
		var buf bytes.Buffer
		cfg := &config.MachineConfig{}
		cfg.Transport.SkipTLSVerify = &tt.skip
		cfg.Transport.CABundleFile = tt.bundle
		c := &Client{cfg: cfg, log: slog.New(slog.NewTextHandler(&buf, nil))}
		c.warnTransport()
		if got := strings.Count(buf.String(), "CA_BUNDLE_FILE has no effect"); got != tt.want {
			t.Errorf("skip=%v bundle=%q: %d warnings, want %d:\n%s", tt.skip, tt.bundle, got, tt.want, buf.String())
		}
	}
}
