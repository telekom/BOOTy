//go:build linux

package rescue

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testHash is `openssl passwd -6 -salt testsalt rescue-pw`.
const (
	testHash     = "$6$testsalt$xS1o3e00o4ssyGlOrd8nk2fjZUsB1BdteI4yWVWI4UCNzmA3S0y628NkbLH31AgcUBLoyxJRoZSCpakWLAixK0"
	testPassword = "rescue-pw"
)

func newTestSigner(t *testing.T, key crypto.Signer) (ssh.Signer, string) {
	t.Helper()
	signer, err := ssh.NewSignerFromSigner(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
}

// newTestKey returns an Ed25519 client key and its authorized_keys line.
func newTestKey(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return newTestSigner(t, priv)
}

// startTestServer serves cfg on a free local port and returns its address.
func startTestServer(t *testing.T, cfg *Config, opk certVerifier) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := newRescueSSH(ctx, cfg)
	if opk != nil {
		s.opk = opk
	}
	srv, _, err := s.serverConfig()
	if err != nil {
		t.Fatal(err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go s.serve(ln, srv)
	return ln.Addr().String()
}

func clientConfig(user string, auth ssh.AuthMethod) *ssh.ClientConfig {
	return &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second} //nolint:gosec // test server
}

func dialRescue(t *testing.T, addr, user string, signer ssh.Signer) *ssh.Client {
	t.Helper()
	c, err := ssh.Dial("tcp", addr, clientConfig(user, ssh.PublicKeys(signer)))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
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

// TestRescueSSHHardened checks the default server against the hardened
// dropbear setup: two keys, key-only, sessions, refusals.
func TestRescueSSHHardened(t *testing.T) {
	a, lineA := newTestKey(t)
	b, lineB := newTestKey(t)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaSigner, rsaLine := newTestSigner(t, rsaKey)
	addr := startTestServer(t, &Config{SSHKeys: []string{lineA, lineB, rsaLine}, PasswordHash: testHash}, nil)

	for _, signer := range []ssh.Signer{a, b} {
		if out := runRescue(t, dialRescue(t, addr, "root", signer), "echo rescue-ok; echo $USER $HOME $PATH"); out !=
			"rescue-ok\nroot /root /usr/sbin:/usr/bin:/sbin:/bin\n" {
			t.Errorf("output %q", out)
		}
	}
	c := dialRescue(t, addr, "root", a)
	assertSessionFeatures(t, c)
	if _, err := c.Dial("tcp", "127.0.0.1:22"); err == nil || !strings.Contains(err.Error(), "prohibited") {
		t.Errorf("local forwarding: %v", err)
	}
	if _, err := c.Listen("tcp", "127.0.0.1:0"); err == nil {
		t.Error("remote forwarding accepted")
	}
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequestSubsystem("sftp"); err == nil {
		t.Error("the SFTP subsystem was accepted")
	}
	for name, cc := range map[string]*ssh.ClientConfig{
		"password with a key set": clientConfig("root", ssh.Password(testPassword)),
		"RSA user key":            clientConfig("root", ssh.PublicKeys(rsaSigner)),
		"not root":                clientConfig("admin", ssh.PublicKeys(a)),
		"group14":                 withConfig(clientConfig("root", ssh.PublicKeys(a)), func(c *ssh.ClientConfig) { c.KeyExchanges = []string{ssh.KeyExchangeDH14SHA256} }),
		"RSA host key":            withConfig(clientConfig("root", ssh.PublicKeys(a)), func(c *ssh.ClientConfig) { c.HostKeyAlgorithms = []string{ssh.KeyAlgoRSASHA256} }),
		"HMAC-SHA1":               withConfig(clientConfig("root", ssh.PublicKeys(a)), func(c *ssh.ClientConfig) { c.MACs = []string{ssh.HMACSHA1}; c.Ciphers = []string{ssh.CipherAES128CTR} }),
	} {
		if _, err := ssh.Dial("tcp", addr, cc); err == nil {
			t.Errorf("%s: login accepted", name)
		}
	}
	assertDisconnectKillsCommand(t, dialRescue(t, addr, "root", a))
}

func withConfig(c *ssh.ClientConfig, f func(*ssh.ClientConfig)) *ssh.ClientConfig {
	f(c)
	return c
}

// assertSessionFeatures checks the PTY, window changes, exit status,
// signals and the refused env request.
func assertSessionFeatures(t *testing.T, c *ssh.Client) {
	t.Helper()
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatalf("pty-req: %v", err)
	}
	if err := s.WindowChange(30, 100); err != nil {
		t.Fatalf("window-change: %v", err)
	}
	if out, err := s.CombinedOutput("tty; echo $TERM $SSH_TTY; stty size"); err != nil || !strings.Contains(string(out), "/dev/pts/") ||
		!strings.Contains(string(out), "xterm /dev/pts/") || !strings.Contains(string(out), "30 100") {
		t.Errorf("PTY session: %v\n%s", err, out)
	}
	if s, err = c.NewSession(); err != nil {
		t.Fatal(err)
	}
	var exitErr *ssh.ExitError
	if err := s.Run("exit 3"); !errors.As(err, &exitErr) || exitErr.ExitStatus() != 3 {
		t.Errorf("exit status: %v", err)
	}
	if s, err = c.NewSession(); err != nil {
		t.Fatal(err)
	}
	if err := s.Setenv("FOO", "bar"); err == nil {
		t.Error("env request accepted")
	}
	out, err := s.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start("echo started; exec sleep 30"); err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 8)
	if _, err := io.ReadFull(out, line); err != nil {
		t.Fatal(err)
	}
	if err := s.Signal(ssh.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := s.Wait(); !errors.As(err, &exitErr) || exitErr.Signal() != "TERM" {
		t.Errorf("signal: %v", err)
	}
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

func TestRescueSSHPasswordLogin(t *testing.T) {
	addr := startTestServer(t, &Config{PasswordHash: testHash}, nil)
	c, err := ssh.Dial("tcp", addr, clientConfig("root", ssh.Password(testPassword)))
	if err != nil {
		t.Fatalf("password login without a key: %v", err)
	}
	if out := runRescue(t, c, "echo rescue-ok"); out != "rescue-ok\n" {
		t.Errorf("output %q", out)
	}
	for user, pw := range map[string]string{"root": "wrong", "admin": testPassword} {
		if _, err := ssh.Dial("tcp", addr, clientConfig(user, ssh.Password(pw))); err == nil {
			t.Errorf("%s/%s accepted", user, pw)
		}
	}
	// Only malformed keys: still key-only.
	addr = startTestServer(t, &Config{SSHKeyConfigured: true, PasswordHash: testHash}, nil)
	if _, err := ssh.Dial("tcp", addr, clientConfig("root", ssh.Password(testPassword))); err == nil {
		t.Error("password login with a malformed key entry")
	}
}

func TestRescueSSHKeyOptions(t *testing.T) {
	forced, forcedLine := newTestKey(t)
	noPTY, noPTYLine := newTestKey(t)
	addr := startTestServer(t, &Config{SSHKeys: []string{
		`command="echo forced:$SSH_ORIGINAL_COMMAND" ` + forcedLine,
		`restrict ` + noPTYLine,
		`from="10.0.0.0/8" ` + forcedLine, // unknown option: unusable, like dropbear
	}}, nil)
	c := dialRescue(t, addr, "root", forced)
	if out := runRescue(t, c, "id"); out != "forced:id\n" {
		t.Errorf("command=: output %q", out)
	}
	c = dialRescue(t, addr, "root", noPTY)
	s, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err == nil {
		t.Error("restrict: pty-req accepted")
	}
	if out := runRescue(t, c, "echo no-pty-ok"); out != "no-pty-ok\n" {
		t.Errorf("restrict: output %q", out)
	}
}

// TestRescueSSHLegacy checks RESCUE_SSH_HARDENED=false: password next to
// keys, RSA keys and host key, group14, local and remote forwarding with
// permitopen=.
func TestRescueSSHLegacy(t *testing.T) {
	echo := startEcho(t)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaSigner, rsaLine := newTestSigner(t, rsaKey)
	limited, limitedLine := newTestKey(t)
	addr := startTestServer(t, &Config{LegacySSH: true, PasswordHash: testHash, SSHKeys: []string{
		rsaLine, `permitopen="127.0.0.1:1" ` + limitedLine}}, nil)

	if _, err := ssh.Dial("tcp", addr, clientConfig("root", ssh.Password(testPassword))); err != nil {
		t.Errorf("password next to keys: %v", err)
	}
	cc := withConfig(clientConfig("root", ssh.PublicKeys(rsaSigner)), func(c *ssh.ClientConfig) {
		c.KeyExchanges = []string{ssh.KeyExchangeDH14SHA256}
		c.HostKeyAlgorithms = []string{ssh.KeyAlgoRSASHA256}
	})
	c, err := ssh.Dial("tcp", addr, cc)
	if err != nil {
		t.Fatalf("RSA key, RSA host key, group14: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if got := roundTrip(t, func() (net.Conn, error) { return c.Dial("tcp", echo) }); got != "ping" {
		t.Errorf("local forward: %q", got)
	}
	ln, err := c.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("remote forward: %v", err)
	}
	go func() {
		if conn, err := ln.Accept(); err == nil {
			_, _ = io.Copy(conn, conn)
			_ = conn.Close()
		}
	}()
	var d net.Dialer
	if got := roundTrip(t, func() (net.Conn, error) { return d.DialContext(context.Background(), "tcp", ln.Addr().String()) }); got != "ping" {
		t.Errorf("remote forward: %q", got)
	}
	if _, err := dialRescue(t, addr, "root", limited).Dial("tcp", echo); err == nil {
		t.Error("permitopen= allowed another destination")
	}
}

func startEcho(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(conn, conn); _ = conn.Close() }()
		}
	}()
	return ln.Addr().String()
}

func roundTrip(t *testing.T, dial func() (net.Conn, error)) string {
	t.Helper()
	conn, err := dial()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

func TestUnauthenticatedLimits(t *testing.T) {
	s := &rescueSSH{unauth: map[string]int{}}
	conn := func(ip string) net.Conn {
		a, b := net.Pipe()
		t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
		return addrConn{a, &net.TCPAddr{IP: net.ParseIP(ip), Port: 1}}
	}
	releases := make([]func(), 0, sshMaxUnauthPerIP)
	for range sshMaxUnauthPerIP {
		releases = append(releases, s.admit(conn("10.0.0.1")))
	}
	if s.admit(conn("10.0.0.1")) != nil {
		t.Error("more than 5 unauthenticated connections of one address")
	}
	releases[0]()
	releases[0]() // idempotent
	if s.admit(conn("10.0.0.1")) == nil {
		t.Error("a released slot is not reused")
	}
	for i := range sshMaxUnauth - sshMaxUnauthPerIP {
		if s.admit(conn(fmt.Sprintf("10.0.1.%d", i))) == nil {
			t.Fatal("refused below the total limit")
		}
	}
	if s.admit(conn("10.0.2.1")) != nil {
		t.Error("more than 30 unauthenticated connections")
	}
}

type addrConn struct {
	net.Conn
	remote net.Addr
}

func (c addrConn) RemoteAddr() net.Addr { return c.remote }

// Setup starts the server (no dropbear any more).
func TestSetupStartsRescueSSH(t *testing.T) {
	old := sshListenAddr
	sshListenAddr = "127.0.0.1:0"
	t.Cleanup(func() { sshListenAddr = old })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := startRescueSSH(ctx, &Config{Mode: ModeShell}); err != nil {
		t.Fatal(err)
	}
}

// TestRescueSSHIgnoresAuthorizedKeysFile plants a key in root's
// authorized_keys (as a deploy archive with PROVISIONER_FILES_TARGET=ramdisk
// could): the server only trusts RESCUE_SSH_PUBKEY, so that key is refused,
// with and without a configured key.
func TestRescueSSHIgnoresAuthorizedKeysFile(t *testing.T) {
	planted, plantedLine := newTestKey(t)
	file := "/root/.ssh/authorized_keys"
	if _, err := os.Stat(file); os.Geteuid() != 0 || err == nil {
		// Not root, or a real file to keep: plant it in the home of this
		// process instead.
		home := t.TempDir()
		t.Setenv("HOME", home)
		file = home + "/.ssh/authorized_keys"
	}
	if err := os.MkdirAll(file[:strings.LastIndex(file, "/")], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(plantedLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(file) })
	configured, configuredLine := newTestKey(t)
	for name, cfg := range map[string]*Config{
		"no key":   {PasswordHash: testHash},
		"with key": {SSHKeys: []string{configuredLine}},
	} {
		addr := startTestServer(t, cfg, nil)
		if _, err := ssh.Dial("tcp", addr, clientConfig("root", ssh.PublicKeys(planted))); err == nil {
			t.Errorf("%s: the key of %s logged in", name, file)
		}
		if name == "with key" {
			_ = dialRescue(t, addr, "root", configured)
		}
	}
}

// TestRescueSSHTerminalModes applies the pty-req modes to the terminal.
func TestRescueSSHTerminalModes(t *testing.T) {
	a, line := newTestKey(t)
	addr := startTestServer(t, &Config{SSHKeys: []string{line}}, nil)
	s, err := dialRescue(t, addr, "root", a).NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPty("xterm", 24, 80, ssh.TerminalModes{ssh.ECHO: 0, ssh.ICANON: 0, ssh.VINTR: 7}); err != nil {
		t.Fatal(err)
	}
	out, err := s.CombinedOutput("stty -a")
	if err != nil {
		t.Fatalf("stty: %v\n%s", err, out)
	}
	for _, want := range []string{"-echo ", "-icanon", "intr = ^G"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("stty -a lacks %q:\n%s", want, out)
		}
	}
}

// TestRescueSSHForwardHalfClose relays a service that answers after the
// end of its request, and closes remote forwards with the connection.
func TestRescueSSHForwardHalfClose(t *testing.T) {
	var lc net.ListenConfig
	svc, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	go func() {
		for {
			conn, err := svc.Accept()
			if err != nil {
				return
			}
			go func() {
				req, _ := io.ReadAll(conn) // until the client's EOF
				_, _ = conn.Write(append([]byte("got "), req...))
				_ = conn.Close()
			}()
		}
	}()
	a, line := newTestKey(t)
	addr := startTestServer(t, &Config{LegacySSH: true, SSHKeys: []string{line}}, nil)
	c := dialRescue(t, addr, "root", a)
	conn, err := c.Dial("tcp", svc.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("req")); err != nil {
		t.Fatal(err)
	}
	if err := conn.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(conn); err != nil || string(got) != "got req" {
		t.Errorf("half-closed local forward: %q, %v", got, err)
	}
	ln, err := c.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fwd := ln.Addr().String()
	_ = c.Close()
	var d net.Dialer
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		conn, err := d.DialContext(context.Background(), "tcp", fwd)
		if err != nil {
			break
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("the remote forward still listens after the client left")
		}
	}
}

// A disconnect ends a local forward also when the destination keeps its
// side open after the EOF and sends nothing: the relay used to wait for it
// forever, with its socket.
func TestRescueSSHForwardDisconnectSilentDestination(t *testing.T) {
	var lc net.ListenConfig
	svc, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, err := svc.Accept(); err == nil {
			accepted <- conn // held open, silent
		}
	}()
	a, line := newTestKey(t)
	addr := startTestServer(t, &Config{LegacySSH: true, SSHKeys: []string{line}}, nil)
	c := dialRescue(t, addr, "root", a)
	if _, err := c.Dial("tcp", svc.Addr().String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = (<-accepted).Close() })
	for deadline := time.Now().Add(5 * time.Second); !relayRunning(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no forward relay")
		}
	}
	_ = c.Close()
	for deadline := time.Now().Add(5 * time.Second); relayRunning(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the forward relay still runs after the client left")
		}
	}
}

// relayRunning reports whether a goroutine is in pipe.
func relayRunning() bool {
	buf := make([]byte, 1<<20)
	return strings.Contains(string(buf[:runtime.Stack(buf, true)]), "rescue.pipe")
}
