package auth

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/telekom/BOOTy/pkg/trust"
)

// The TOKEN_URL client trusts the CA_BUNDLE_FILE roots.
func TestTokenManagerTrustsCABundle(t *testing.T) {
	t.Cleanup(trust.Reset)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(TokenResponse{AccessToken: "jwt", ExpiresIn: 3600, TokenType: "Bearer"})
	}))
	defer srv.Close()

	trust.Reset()
	if err := mustNewTokenManager(t, srv.URL, "boot", slog.Default()).Acquire(context.Background(), "SN", "aa:bb:cc:dd:ee:ff"); err == nil {
		t.Fatal("Acquire() succeeded against an untrusted certificate")
	}
	bundle := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := trust.LoadFile(bundle); err != nil {
		t.Fatal(err)
	}
	if err := mustNewTokenManager(t, srv.URL, "boot", slog.Default()).Acquire(context.Background(), "SN", "aa:bb:cc:dd:ee:ff"); err != nil {
		t.Fatalf("Acquire() with the CA bundle = %v", err)
	}
}
