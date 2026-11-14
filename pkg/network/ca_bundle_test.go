package network

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/telekom/BOOTy/pkg/trust"
)

// NewHTTPClient trusts the CA_BUNDLE_FILE roots of trust.LoadFile.
func TestNewHTTPClientTrustsCABundle(t *testing.T) {
	oldSkip := SkipTLSVerify()
	t.Cleanup(func() {
		trust.Reset()
		SetSkipTLSVerify(oldSkip)
	})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	SetSkipTLSVerify(false)

	trust.Reset()
	if err := WaitForHTTP(context.Background(), srv.URL, 1500*time.Millisecond); err == nil {
		t.Fatal("WaitForHTTP() succeeded against an untrusted certificate")
	}
	bundle := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := trust.LoadFile(bundle); err != nil {
		t.Fatal(err)
	}
	if err := WaitForHTTP(context.Background(), srv.URL, 5*time.Second); err != nil {
		t.Fatalf("WaitForHTTP() with the CA bundle = %v", err)
	}
}
