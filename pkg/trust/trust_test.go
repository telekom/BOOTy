package trust

import (
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func certPEM(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
}

func writeBundle(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ca-bundle.pem")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParsePEM(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	cert := certPEM(t, srv)
	key := "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"
	broken := "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"
	for _, tt := range []struct {
		name    string
		data    string
		want    int
		wantErr string
	}{
		{name: "one", data: cert, want: 1},
		{name: "two with text before each", data: "# root 1\n" + cert + "# root 2\n" + cert, want: 2},
		{name: "empty", data: "", wantErr: "no PEM certificate"},
		{name: "only text", data: "not a certificate\n", wantErr: "not a PEM certificate"},
		{name: "private key", data: cert + key, wantErr: `"PRIVATE KEY"`},
		{name: "broken DER", data: broken, wantErr: "PEM block 1"},
		{name: "truncated", data: cert + cert[:len(cert)/2], wantErr: "after PEM block 1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			certs, err := ParsePEM([]byte(tt.data))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParsePEM() = %v, want error with %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || len(certs) != tt.want {
				t.Fatalf("ParsePEM() = %d certs, %v; want %d", len(certs), err, tt.want)
			}
		})
	}
}

func TestLoadFile(t *testing.T) {
	t.Cleanup(Reset)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	get := func() error {
		c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: Roots()}}}
		resp, err := c.Get(srv.URL) //nolint:noctx // test
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}

	Reset()
	if err := LoadFile(""); err != nil || Roots() != nil {
		t.Fatalf("LoadFile(\"\") = %v, roots %v; want nil, system roots", err, Roots())
	}
	if err := get(); err == nil {
		t.Fatal("GET succeeded against an untrusted certificate")
	}
	for _, bad := range []string{filepath.Join(t.TempDir(), "missing.pem"), writeBundle(t, "garbage")} {
		if err := LoadFile(bad); err == nil {
			t.Fatalf("LoadFile(%s) succeeded", bad)
		}
		if Roots() != nil {
			t.Fatal("a failed LoadFile changed the roots")
		}
	}
	if err := LoadFile(writeBundle(t, certPEM(t, srv))); err != nil {
		t.Fatalf("LoadFile() = %v", err)
	}
	if err := get(); err != nil {
		t.Fatalf("GET with the CA bundle = %v", err)
	}
}
