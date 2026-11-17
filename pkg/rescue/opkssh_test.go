//go:build linux && !no_opkssh

package rescue

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/telekom/BOOTy/pkg/rescue/opktest"
)

// startMockOIDC serves a mock provider over HTTPS and returns it with a
// client that trusts the server.
func startMockOIDC(t *testing.T, clientID string, configure ...func(*opktest.Provider)) (*opktest.Provider, *http.Client) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	prov, err := opktest.NewProvider("https://"+ln.Addr().String(), clientID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range configure {
		f(prov)
	}
	srv := httptest.NewUnstartedServer(prov)
	srv.Listener = ln
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return prov, httpsOnly(srv.Client())
}

func TestOPKVerify(t *testing.T) {
	ctx := context.Background()
	prov, httpClient := startMockOIDC(t, "booty")
	// The same issuer with other signing keys: an unknown key.
	stranger, err := opktest.NewProvider(prov.Issuer, "booty")
	if err != nil {
		t.Fatal(err)
	}
	other, err := opktest.NewProvider("https://other.example", "booty")
	if err != nil {
		t.Fatal(err)
	}
	policy := parseOPKPolicy("root alice@example.com " + prov.Issuer + "; root oidc:groups:ops " + prov.Issuer)
	newVerifier := func(clientID, expiration string) *opkVerifier {
		v, err := newOPKVerifier(parseOPKProviders(prov.Issuer+" "+clientID+" "+expiration), policy, httpClient)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	now := time.Now()
	alice := map[string]any{"email": "alice@example.com", "email_verified": true}
	for _, tc := range []struct {
		name       string
		issuer     *opktest.Provider
		claims     map[string]any
		clientID   string
		expiration string
		user       string
		want       string // "" accepts
	}{
		{name: "email in the policy", claims: alice},
		{name: "group in the policy", claims: map[string]any{"email": "bob@example.com", "groups": []string{"dev", "ops"}}},
		{name: "expired ID token", claims: map[string]any{"email": "alice@example.com", "email_verified": true, "iat": now.Add(-2 * time.Hour).Unix(),
			"exp": now.Add(-time.Hour).Unix()}, expiration: "oidc", want: "expired"},
		{name: "older than the max age", claims: map[string]any{"email": "alice@example.com", "email_verified": true, "iat": now.Add(-13 * time.Hour).Unix()},
			expiration: "12h", want: "expired"},
		{name: "unknown issuer", issuer: other, claims: alice, want: "unrecognized issuer"},
		{name: "wrong client ID", claims: alice, clientID: "someone-else", want: "audience"},
		{name: "no policy row", claims: map[string]any{"email": "mallory@example.com", "email_verified": true}, want: "no RESCUE_SSH_OPK_POLICY row"},
		{name: "unverified email", claims: map[string]any{"email": "alice@example.com", "email_verified": false}, want: "no RESCUE_SSH_OPK_POLICY row"},
		{name: "email without email_verified", claims: map[string]any{"email": "alice@example.com"}, want: "no RESCUE_SSH_OPK_POLICY row"},
		{name: "string email_verified", claims: map[string]any{"email": "alice@example.com", "email_verified": "true"}},
		{name: "unknown signing key", issuer: stranger, claims: alice, want: "key"},
		{name: "not root", claims: alice, user: "alice", want: "only root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issuer, clientID, expiration, user := prov, "booty", "24h", "root"
			if tc.issuer != nil {
				issuer = tc.issuer
			}
			if tc.clientID != "" {
				clientID = tc.clientID
			}
			if tc.expiration != "" {
				expiration = tc.expiration
			}
			if tc.user != "" {
				user = tc.user
			}
			key, err := issuer.NewKey(ctx, tc.claims)
			if err != nil {
				t.Fatal(err)
			}
			claims, err := newVerifier(clientID, expiration).verify(ctx, user, key.Cert)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.want != "" && err == nil:
				t.Fatal("accepted")
			case tc.want != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if claims.Issuer != issuer.Issuer {
				t.Errorf("logged issuer %q, want %q", claims.Issuer, issuer.Issuer)
			}
		})
	}
}

// The certificate principals are the client's choice: they grant nothing,
// the policy decides. The validity period, the signature and the critical
// options are still checked.
func TestOPKVerifyCertificate(t *testing.T) {
	ctx := context.Background()
	prov, httpClient := startMockOIDC(t, "booty")
	v, err := newOPKVerifier(parseOPKProviders(prov.Issuer+" booty 24h"),
		parseOPKPolicy("root alice@example.com "+prov.Issuer), httpClient)
	if err != nil {
		t.Fatal(err)
	}
	alice := map[string]any{"email": "alice@example.com", "email_verified": true}
	mallory := map[string]any{"email": "mallory@example.com", "email_verified": true}
	for _, tc := range []struct {
		name   string
		claims map[string]any
		user   string
		edit   func(*ssh.Certificate) // before signing
		tamper func(*ssh.Certificate) // after signing
		want   string                 // "" accepts
	}{
		{name: "principal root, identity not in the policy", claims: mallory,
			edit: func(c *ssh.Certificate) { c.ValidPrincipals = []string{"root"} }, want: "no RESCUE_SSH_OPK_POLICY row"},
		{name: "other principal, identity not in the policy", claims: mallory,
			edit: func(c *ssh.Certificate) { c.ValidPrincipals = []string{"alice"} }, want: "no RESCUE_SSH_OPK_POLICY row"},
		{name: "other principal, identity in the policy", claims: alice,
			edit: func(c *ssh.Certificate) { c.ValidPrincipals = []string{"alice", "root"} }},
		{name: "principal of the user, not root", claims: alice, user: "alice",
			edit: func(c *ssh.Certificate) { c.ValidPrincipals = []string{"alice"} }, want: "only root"},
		{name: "no principals", claims: alice, tamper: func(c *ssh.Certificate) { c.ValidPrincipals = nil }, want: "no principals"},
		{name: "expired", claims: alice, edit: func(c *ssh.Certificate) { c.ValidBefore = 1 /* 1970 */ }, want: "expired"},
		{name: "not yet valid", claims: alice, edit: func(c *ssh.Certificate) { c.ValidAfter = 1 << 62 }, want: "not yet valid"},
		{name: "force-command", claims: alice,
			edit: func(c *ssh.Certificate) { c.CriticalOptions = map[string]string{"force-command": "true"} }, want: "critical option"},
		{name: "source-address", claims: alice,
			edit: func(c *ssh.Certificate) { c.CriticalOptions = map[string]string{"source-address": "192.0.2.1/32"} }, want: "critical option"},
		{name: "altered after signing", claims: alice, tamper: func(c *ssh.Certificate) { c.ValidPrincipals = []string{"root"} },
			want: "signature"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, err := prov.NewKey(ctx, tc.claims)
			if err != nil {
				t.Fatal(err)
			}
			cert := *key.Cert
			if tc.edit != nil {
				tc.edit(&cert)
				signer, err := ssh.NewSignerFromSigner(key.Private)
				if err != nil {
					t.Fatal(err)
				}
				if err := cert.SignCert(rand.Reader, signer); err != nil {
					t.Fatal(err)
				}
			}
			if tc.tamper != nil {
				tc.tamper(&cert)
			}
			user := "root"
			if tc.user != "" {
				user = tc.user
			}
			_, err = v.verify(ctx, user, &cert)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.want != "" && err == nil:
				t.Fatal("accepted")
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParseOPKTables(t *testing.T) {
	got := parseOPKProviders("https://a.example c 24h; http://b.example c 24h; https://c.example c never;" +
		"https://d.example c oidc_refreshed; https://e.example c; # comment\nhttps://f.example c oidc")
	if len(got) != 2 || got[0].issuer != "https://a.example" || got[1].issuer != "https://f.example" {
		t.Fatalf("providers = %+v", got)
	}
	rules := parseOPKPolicy("root a@example.com https://a.example;root oidc-match-end:email:@example.com https://a.example;" +
		"root oidc:groups https://a.example; root sub-1; root oidc:groups:ops https://a.example")
	if len(rules) != 2 || rules[1].identity != "oidc:groups:ops" {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestRefusalReasonHidesTokens(t *testing.T) {
	reason := refusalReason(errors.New("bad token eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJtZSJ9.c2ln here"))
	if strings.Contains(reason, "eyJ") || !strings.Contains(reason, "<token> here") {
		t.Fatalf("reason = %q", reason)
	}
}

// TestRescueSSHOPKLogin logs in over SSH with an opkssh key next to a
// static key and checks the refusals.
func TestRescueSSHOPKLogin(t *testing.T) {
	ctx := context.Background()
	prov, httpClient := startMockOIDC(t, "booty")
	v, err := newOPKVerifier(parseOPKProviders(prov.Issuer+" booty 12h"),
		parseOPKPolicy("root alice@example.com "+prov.Issuer), httpClient)
	if err != nil {
		t.Fatal(err)
	}
	staticSigner, staticLine := newTestKey(t)
	addr := startTestServer(t, &Config{SSHKeys: []string{staticLine}, OPKPolicy: "x", PasswordHash: testHash}, v.check)

	for name, claims := range map[string]map[string]any{
		"valid": {"email": "alice@example.com", "email_verified": true},
	} {
		key, err := prov.NewKey(ctx, claims)
		if err != nil {
			t.Fatal(err)
		}
		signer, err := key.Signer()
		if err != nil {
			t.Fatal(err)
		}
		if out := runRescue(t, dialRescue(t, addr, "root", signer), "echo rescue-ok"); out != "rescue-ok\n" {
			t.Errorf("%s: output %q", name, out)
		}
	}
	if out := runRescue(t, dialRescue(t, addr, "root", staticSigner), "echo rescue-ok"); out != "rescue-ok\n" {
		t.Errorf("static key: output %q", out)
	}
	for name, claims := range map[string]map[string]any{
		"expired PK Token": {"email": "alice@example.com", "email_verified": true, "iat": time.Now().Add(-13 * time.Hour).Unix()},
		"no policy row":    {"email": "mallory@example.com", "email_verified": true},
	} {
		key, err := prov.NewKey(ctx, claims)
		if err != nil {
			t.Fatal(err)
		}
		signer, err := key.Signer()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ssh.Dial("tcp", addr, clientConfig("root", ssh.PublicKeys(signer))); err == nil {
			t.Errorf("%s: login accepted", name)
		}
	}
	// opkssh settings make the login key-only.
	if _, err := ssh.Dial("tcp", addr, clientConfig("root", ssh.Password(testPassword))); err == nil {
		t.Error("password login next to opkssh settings")
	}
}

// A discovery document that names an http:// JWKS URI is refused: the keys
// would come over plain HTTP.
func TestOPKVerifyRefusesHTTPJWKS(t *testing.T) {
	ctx := context.Background()
	prov, httpClient := startMockOIDC(t, "booty", func(p *opktest.Provider) {
		p.JWKSURI = strings.Replace(p.Issuer, "https://", "http://", 1) + "/jwks"
	})
	key, err := prov.NewKey(ctx, map[string]any{"email": "alice@example.com", "email_verified": true})
	if err != nil {
		t.Fatal(err)
	}
	v, err := newOPKVerifier(parseOPKProviders(prov.Issuer+" booty 24h"),
		parseOPKPolicy("root alice@example.com "+prov.Issuer), httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.verify(ctx, "root", key.Cert); err == nil || !strings.Contains(err.Error(), "not https") {
		t.Fatalf("err = %v, want the https refusal", err)
	}
}

// selfSignedOPKCert returns a certificate of signer that carries
// pkt as its openpubkey-pkt extension, as anyone can make one.
func selfSignedOPKCert(tb testing.TB, signer ssh.Signer, pkt string) *ssh.Certificate {
	tb.Helper()
	cert := &ssh.Certificate{Key: signer.PublicKey(), CertType: ssh.UserCert, ValidBefore: ssh.CertTimeInfinity,
		ValidPrincipals: []string{"opkssh-wildcard"},
		Permissions:     ssh.Permissions{Extensions: map[string]string{"openpubkey-pkt": pkt}}}
	if err := cert.SignCert(rand.Reader, signer); err != nil {
		tb.Fatal(err)
	}
	return cert
}

// A PK Token without a CIC signature made openpubkey dereference nil while
// the client had not logged in: the panic stopped BOOTy. The login must be
// refused and the server keep serving.
func TestRescueSSHOPKMalformedPKToken(t *testing.T) {
	prov, httpClient := startMockOIDC(t, "booty")
	v, err := newOPKVerifier(parseOPKProviders(prov.Issuer+" booty 24h"),
		parseOPKPolicy("root alice@example.com "+prov.Issuer), httpClient)
	if err != nil {
		t.Fatal(err)
	}
	staticSigner, staticLine := newTestKey(t)
	addr := startTestServer(t, &Config{SSHKeys: []string{staticLine}, OPKPolicy: "x"}, v.check)
	b64 := base64.RawURLEncoding.EncodeToString
	opOnly := b64([]byte(`{"iss":"`+prov.Issuer+`"}`)) + ":" + b64([]byte(`{"alg":"RS256"}`)) + ":" + b64([]byte("sig"))
	signer, _ := newTestKey(t)
	certSigner, err := ssh.NewCertSigner(selfSignedOPKCert(t, signer, opOnly), signer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ssh.Dial("tcp", addr, clientConfig("root", ssh.PublicKeys(certSigner))); err == nil {
		t.Fatal("login with a PK Token without a CIC signature accepted")
	}
	if out := runRescue(t, dialRescue(t, addr, "root", staticSigner), "echo rescue-ok"); out != "rescue-ok\n" {
		t.Errorf("static key after the refusal: output %q", out)
	}
}

// FuzzOPKCheck feeds arbitrary openpubkey-pkt extensions, which a client
// sends before it logs in, to the verifier: it must refuse without a
// panic.
func FuzzOPKCheck(f *testing.F) {
	prov, err := opktest.NewProvider("https://idp.example", "booty")
	if err != nil {
		f.Fatal(err)
	}
	key, err := prov.NewKey(context.Background(), map[string]any{"email": "alice@example.com", "email_verified": true})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(key.Cert.Extensions["openpubkey-pkt"])
	f.Add("e30:e30:e30")
	f.Add("e30:e30:e30:e30:e30.a.b.c")
	// No JWKS: a token that gets that far fails at the fetch.
	client := httpsOnly(&http.Client{Transport: http.NewFileTransport(http.Dir("/nonexistent"))})
	v, err := newOPKVerifier(parseOPKProviders(prov.Issuer+" booty 24h"),
		parseOPKPolicy("root alice@example.com "+prov.Issuer), client)
	if err != nil {
		f.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		f.Fatal(err)
	}
	// verify, not check: check turns a panic into a refusal.
	f.Fuzz(func(t *testing.T, pkt string) {
		if _, err := v.verify(context.Background(), "root", selfSignedOPKCert(t, signer, pkt)); err == nil {
			t.Fatalf("PK Token %q accepted", pkt)
		}
	})
}
