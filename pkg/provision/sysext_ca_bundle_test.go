//go:build linux

package provision

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/telekom/BOOTy/pkg/network"
	"github.com/telekom/BOOTy/pkg/trust"
)

// RefreshHTTPClients gives the sysext client the CA_BUNDLE_FILE roots and
// keeps its TLS check on with SKIP_TLS_VERIFY, like the client built at start.
func TestRefreshHTTPClientsSysextTrust(t *testing.T) {
	old := sysextHTTPClient
	t.Cleanup(func() {
		sysextHTTPClient = old
		network.SetSkipTLSVerify(false)
		trust.Reset()
	})
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	srv.Close()
	bundle := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := trust.LoadFile(bundle); err != nil {
		t.Fatal(err)
	}
	network.SetSkipTLSVerify(true)
	RefreshHTTPClients()
	tr, ok := sysextHTTPClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", sysextHTTPClient.Transport)
	}
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("sysext client skips the TLS check")
	}
	if tr.TLSClientConfig.RootCAs == nil || tr.TLSClientConfig.RootCAs != trust.Roots() {
		t.Fatal("sysext client does not use trust.Roots()")
	}
}
