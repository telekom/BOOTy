package image

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/telekom/BOOTy/pkg/network"
	"github.com/telekom/BOOTy/pkg/trust"
)

// The image clients and the OCI transport trust the CA_BUNDLE_FILE roots.
func TestCABundleImageClients(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "image")
	}))
	defer srv.Close()
	oldBackoff := retryBackoffBase
	retryBackoffBase = time.Millisecond
	oldSkip := network.SkipTLSVerify()
	t.Cleanup(func() {
		retryBackoffBase = oldBackoff
		network.SetSkipTLSVerify(oldSkip)
		trust.Reset()
		RefreshHTTPClients()
	})
	network.SetSkipTLSVerify(false)

	trust.Reset()
	RefreshHTTPClients()
	if ociTransport() != remote.DefaultTransport {
		t.Fatal("ociTransport() without a CA bundle is not remote.DefaultTransport")
	}
	if body, err := httpGetWithRetry(context.Background(), srv.URL); err == nil {
		_ = body.Close()
		t.Fatal("image GET succeeded against an untrusted certificate")
	}

	bundle := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := trust.LoadFile(bundle); err != nil {
		t.Fatal(err)
	}
	RefreshHTTPClients()
	body, err := httpGetWithRetry(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("image GET with the CA bundle = %v", err)
	}
	_ = body.Close()
	if _, err := probeURL(context.Background(), srv.URL); err != nil {
		t.Fatalf("probe with the CA bundle = %v", err)
	}

	if ociTransport() != ociTransport() {
		t.Error("ociTransport() is not cached for the same roots")
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	resp, err := ociTransport().RoundTrip(req)
	if err != nil {
		t.Fatalf("OCI transport with the CA bundle = %v", err)
	}
	_ = resp.Body.Close()
}
