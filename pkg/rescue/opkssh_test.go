//go:build linux && !no_opkssh

package rescue

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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

// TestRescueSSHServer logs in over SSH with an opkssh key and a static key
// and checks the refusals of the hardened server.
func TestRescueSSHServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	prov, httpClient := startMockOIDC(t, "booty")
	v, err := newOPKVerifier(parseOPKProviders(prov.Issuer+" booty 12h"),
		parseOPKPolicy("root alice@example.com "+prov.Issuer), httpClient)
	if err != nil {
		t.Fatal(err)
	}
	staticSigner, staticAuthorized := newStaticKey(t)
	auth := &rescueSSHAuth{ctx: ctx, opk: v, static: staticSSHKeys([]string{staticAuthorized})}
	srv, _, err := newSSHServerConfig(auth)
	if err != nil {
		t.Fatal(err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go serveSSH(ctx, ln, srv)

	good, err := prov.NewKey(ctx, map[string]any{"email": "alice@example.com", "email_verified": true})
	if err != nil {
		t.Fatal(err)
	}
	goodSigner, err := good.Signer()
	if err != nil {
		t.Fatal(err)
	}
	for name, signer := range map[string]ssh.Signer{"opkssh": goodSigner, "static": staticSigner} {
		c := dialRescue(t, ln.Addr().String(), "root", signer)
		out := runRescue(t, c, "echo rescue-ok")
		if out != "rescue-ok\n" {
			t.Errorf("%s: output %q", name, out)
		}
		if _, err := c.Dial("tcp", "127.0.0.1:22"); err == nil || !strings.Contains(err.Error(), "prohibited") {
			t.Errorf("%s: port forwarding: %v", name, err)
		}
		s, err := c.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
			t.Fatalf("%s: pty-req: %v", name, err)
		}
		if out, err := s.CombinedOutput("tty; echo $TERM; stty size"); err != nil || !strings.Contains(string(out), "/dev/pts/") ||
			!strings.Contains(string(out), "xterm") || !strings.Contains(string(out), "24 80") {
			t.Errorf("%s: PTY session: %v\n%s", name, err, out)
		}
		if s, err = c.NewSession(); err != nil {
			t.Fatal(err)
		}
		if err := s.RequestSubsystem("sftp"); err == nil {
			t.Errorf("%s: the SFTP subsystem was accepted", name)
		}
		_ = c.Close()
	}

	expired, err := prov.NewKey(ctx, map[string]any{"email": "alice@example.com", "email_verified": true, "iat": time.Now().Add(-13 * time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	expiredSigner, err := expired.Signer()
	if err != nil {
		t.Fatal(err)
	}
	for name, signer := range map[string]ssh.Signer{"expired PK Token": expiredSigner} {
		if _, err := ssh.Dial("tcp", ln.Addr().String(), clientConfig("root", signer)); err == nil {
			t.Errorf("%s: login accepted", name)
		}
	}
	if _, err := ssh.Dial("tcp", ln.Addr().String(), clientConfig("admin", staticSigner)); err == nil {
		t.Error("a static key logged in as admin")
	}
	assertDisconnectKillsCommand(t, dialRescue(t, ln.Addr().String(), "root", staticSigner))
}

// assertDisconnectKillsCommand starts a long command, drops the connection
// and waits for the command to end.
func assertDisconnectKillsCommand(t *testing.T, c *ssh.Client) {
	t.Helper()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start("echo $$; sleep 300"); err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Fscan(out, &pid); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); os.IsNotExist(err) {
			return
		}
	}
	t.Fatalf("command %d still runs after the client left", pid)
}

func newStaticKey(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	prov, err := opktest.NewProvider("https://unused.example", "x")
	if err != nil {
		t.Fatal(err)
	}
	key, err := prov.NewKey(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromSigner(key.Private)
	if err != nil {
		t.Fatal(err)
	}
	return signer, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
}

func clientConfig(user string, signer ssh.Signer) *ssh.ClientConfig {
	return &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second} //nolint:gosec // test server
}

func dialRescue(t *testing.T, addr, user string, signer ssh.Signer) *ssh.Client {
	t.Helper()
	c, err := ssh.Dial("tcp", addr, clientConfig(user, signer))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return c
}

func runRescue(t *testing.T, c *ssh.Client, cmd string) string {
	t.Helper()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	out, err := s.CombinedOutput(cmd)
	if err != nil {
		t.Fatalf("%s: %v\n%s", cmd, err, out)
	}
	return string(out)
}

// Without the opkssh settings BOOTy starts dropbear as before; with one it
// serves SSH itself and never looks for dropbear.
func TestStartSSHChoosesServer(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := startSSH(context.Background(), &Config{Mode: ModeShell}); err == nil || !strings.Contains(err.Error(), "dropbear") {
		t.Fatalf("without opkssh settings: %v, want the dropbear lookup", err)
	}
	old := sshListenAddr
	sshListenAddr = "127.0.0.1:0"
	t.Cleanup(func() { sshListenAddr = old })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := startSSH(ctx, &Config{Mode: ModeShell, OPKPolicy: "root a@example.com https://idp.example"}); err != nil {
		t.Fatalf("with an opkssh setting: %v", err)
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
