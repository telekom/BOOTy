//go:build linux && !no_opkssh

package rescue

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"

	exec "github.com/telekom/BOOTy/pkg/executil"
	"github.com/telekom/BOOTy/pkg/timesync"
)

// The rescue SSH server of the opkssh settings. It has the hardening of the
// dropbear setup: an Ed25519 host key per boot, curve25519/ML-KEM key
// exchange and AEAD ciphers only, no RSA, key-only login, no forwarding, no
// SFTP and the 15 minute idle timeout.
// sshListenAddr is a variable for tests.
var sshListenAddr = ":22"

const (
	sshAuthTimeout = 30 * time.Second
	sshIdle        = 900 * time.Second
	rescueShell    = "/bin/sh"
)

// sshUserKeyAlgorithms are the user key signature algorithms, also of the
// key in a certificate: no RSA, like the hardened dropbear.
var sshUserKeyAlgorithms = []string{
	ssh.KeyAlgoED25519, ssh.KeyAlgoSKED25519,
	ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoSKECDSA256,
}

// rescueSSHAuth decides the public key logins.
type rescueSSHAuth struct {
	ctx    context.Context
	opk    *opkVerifier // nil without a valid provider
	static [][]byte     // wire form of the RESCUE_SSH_PUBKEY keys
}

// startOPKSSH serves rescue SSH on port 22 until ctx ends.
func startOPKSSH(ctx context.Context, cfg *Config) error {
	auth := &rescueSSHAuth{ctx: ctx, static: staticSSHKeys(cfg.SSHKeys)}
	if !timesync.Synced() {
		slog.Error("rescue SSH: the clock was not synced (TIME_SYNC), so the PK Token times are checked against the RTC")
	}
	v, err := newOPKVerifier(parseOPKProviders(cfg.OPKProviders), parseOPKPolicy(cfg.OPKPolicy), jwksHTTPClient())
	if err != nil {
		slog.Warn("rescue SSH accepts no opkssh login", "error", err)
	} else {
		auth.opk = v
	}
	srv, fingerprint, err := newSSHServerConfig(auth)
	if err != nil {
		return err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", sshListenAddr)
	if err != nil {
		return fmt.Errorf("rescue SSH listen: %w", err)
	}
	go func() { <-ctx.Done(); _ = ln.Close() }()
	go serveSSH(ctx, ln, srv)
	slog.Info("rescue SSH server started", "opkssh", auth.opk != nil, "static_keys", len(auth.static), "host_key", fingerprint)
	fmt.Println("SSH daemon started on port 22 (BOOTy opkssh server)") //nolint:forbidigo // rescue mode output
	return nil
}

// newSSHServerConfig returns the server config with a new Ed25519 host key
// and the SHA-256 fingerprint of that key.
func newSSHServerConfig(auth *rescueSSHAuth) (*ssh.ServerConfig, string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("rescue SSH host key: %w", err)
	}
	host, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, "", fmt.Errorf("rescue SSH host key: %w", err)
	}
	srv := &ssh.ServerConfig{
		Config: ssh.Config{
			KeyExchanges: []string{ssh.KeyExchangeMLKEM768X25519, ssh.KeyExchangeCurve25519, "curve25519-sha256@libssh.org",
				ssh.KeyExchangeECDHP256, ssh.KeyExchangeECDHP384, ssh.KeyExchangeECDHP521},
			Ciphers: []string{ssh.CipherChaCha20Poly1305, ssh.CipherAES256GCM, ssh.CipherAES128GCM},
			MACs:    []string{ssh.HMACSHA256ETM, ssh.HMACSHA512ETM},
		},
		PublicKeyCallback:       auth.publicKey,
		PublicKeyAuthAlgorithms: sshUserKeyAlgorithms,
		ServerVersion:           "SSH-2.0-BOOTy",
	}
	srv.AddHostKey(host)
	return srv, ssh.FingerprintSHA256(host.PublicKey()), nil
}

// staticSSHKeys returns the wire form of the RESCUE_SSH_PUBKEY keys. This
// server does not apply authorized_keys options, so it skips keys with
// options instead of dropping their restrictions.
func staticSSHKeys(entries []string) [][]byte {
	var out [][]byte
	for i, e := range entries {
		key, _, options, _, err := ssh.ParseAuthorizedKey([]byte(e))
		switch {
		case err != nil:
			slog.Warn("ignoring malformed rescue SSH key", "entry", i+1)
		case len(options) > 0:
			slog.Warn("ignoring rescue SSH key with options, which the opkssh rescue server does not apply", "entry", i+1)
		case !slices.Contains(sshUserKeyAlgorithms, key.Type()):
			slog.Warn("ignoring rescue SSH key of a type that the rescue server refuses", "entry", i+1, "type", key.Type())
		default:
			out = append(out, key.Marshal())
		}
	}
	return out
}

// errLoginRefused is the error the SSH client gets; the reason is only in
// the log.
var errLoginRefused = errors.New("rescue SSH login refused")

// publicKey accepts a static key or an opkssh certificate for root. It
// logs every refusal; the accepted login is logged after the handshake,
// when the client has proven that it holds the key.
func (a *rescueSSHAuth) publicKey(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	log := slog.With("principal", meta.User(), "remote", meta.RemoteAddr().String())
	if cert, ok := key.(*ssh.Certificate); ok && cert.Extensions["openpubkey-pkt"] != "" {
		if a.opk == nil {
			log.Warn("rescue SSH login refused", "method", "opkssh", "reason", "no valid opkssh provider configured")
			return nil, errLoginRefused
		}
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()
		claims, err := a.opk.verify(ctx, meta.User(), cert)
		if err != nil {
			log.Warn("rescue SSH login refused", "method", "opkssh", "issuer", claims.Issuer, "subject", claims.Subject,
				"reason", refusalReason(err))
			return nil, errLoginRefused
		}
		return &ssh.Permissions{Extensions: map[string]string{"method": "opkssh", "issuer": claims.Issuer, "subject": claims.Subject}}, nil
	}
	fp := ssh.FingerprintSHA256(key)
	wire := key.Marshal()
	switch {
	case meta.User() != rescueUser:
		log.Warn("rescue SSH login refused", "method", "publickey", "key", fp, "reason", "only "+rescueUser+" can log in")
	case !slices.ContainsFunc(a.static, func(k []byte) bool { return bytes.Equal(k, wire) }):
		log.Warn("rescue SSH login refused", "method", "publickey", "key", fp, "reason", "not a RESCUE_SSH_PUBKEY key")
	default:
		return &ssh.Permissions{Extensions: map[string]string{"method": "publickey", "key": fp}}, nil
	}
	return nil, errLoginRefused
}

// serveSSH accepts connections until ln is closed.
func serveSSH(ctx context.Context, ln net.Listener, srv *ssh.ServerConfig) {
	for {
		nc, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				slog.Warn("rescue SSH server stopped", "error", err)
			}
			return
		}
		go serveSSHConn(ctx, nc, srv)
	}
}

// idleConn closes a connection without traffic for timeout. Before the
// login, timeout is 0 and the fixed deadline of the handshake applies.
type idleConn struct {
	net.Conn
	timeout atomic.Int64
}

func (c *idleConn) extend() {
	if t := time.Duration(c.timeout.Load()); t > 0 {
		_ = c.SetDeadline(time.Now().Add(t))
	}
}

func (c *idleConn) Read(b []byte) (int, error) {
	c.extend()
	return c.Conn.Read(b) //nolint:wrapcheck // a net.Conn passes its errors through
}

func (c *idleConn) Write(b []byte) (int, error) {
	c.extend()
	return c.Conn.Write(b) //nolint:wrapcheck // a net.Conn passes its errors through
}

func serveSSHConn(ctx context.Context, nc net.Conn, srv *ssh.ServerConfig) {
	ic := &idleConn{Conn: nc}
	_ = nc.SetDeadline(time.Now().Add(sshAuthTimeout))
	conn, chans, reqs, err := ssh.NewServerConn(ic, srv)
	if err != nil {
		slog.Debug("rescue SSH connection closed before a login", "remote", nc.RemoteAddr().String(), "error", err)
		_ = nc.Close()
		return
	}
	ic.timeout.Store(int64(sshIdle))
	ic.extend()
	defer func() { _ = conn.Close() }()
	attrs := []any{"principal", conn.User(), "remote", conn.RemoteAddr().String()}
	for _, k := range []string{"method", "issuer", "subject", "key"} {
		if v := conn.Permissions.Extensions[k]; v != "" {
			attrs = append(attrs, k, v)
		}
	}
	slog.Info("rescue SSH login accepted", attrs...)
	go ssh.DiscardRequests(reqs) // tcpip-forward and the other global requests are refused
	for nch := range chans {
		if nch.ChannelType() != "session" {
			_ = nch.Reject(ssh.Prohibited, "only sessions are allowed")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go serveSession(ctx, ch, creqs)
	}
}

// session is one SSH session channel: a shell or a command, with or
// without a PTY.
type session struct {
	ctx     context.Context
	ch      ssh.Channel
	term    string
	pty     *os.File // master side; nil without a pty-req
	started bool
}

func serveSession(ctx context.Context, ch ssh.Channel, reqs <-chan *ssh.Request) {
	s := &session{ctx: ctx, ch: ch}
	defer func() {
		if s.pty != nil {
			_ = s.pty.Close()
		}
	}()
	for req := range reqs {
		ok := false
		switch req.Type {
		case "pty-req":
			ok = !s.started && s.pty == nil && s.openPTY(req.Payload) == nil
		case "window-change":
			ok = s.pty != nil && setWinsize(s.pty, req.Payload) == nil
		case "shell", "exec":
			if !s.started {
				err := s.start(req)
				if err != nil {
					slog.Warn("rescue SSH session did not start", "error", err)
				}
				ok = err == nil
			}
		}
		// Refused: env, subsystem (sftp), x11-req, auth-agent-req and the rest.
		if req.WantReply {
			_ = req.Reply(ok, nil)
		}
	}
}

// openPTY parses a pty-req (term, cols, rows, ...) and opens a pseudo
// terminal of that size.
func (s *session) openPTY(payload []byte) error {
	var req struct {
		Term                 string
		Cols, Rows, PxW, PxH uint32
		Modes                string
	}
	if err := ssh.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("pty-req: %w", err)
	}
	if err := mountDevpts(); err != nil {
		return err
	}
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return fmt.Errorf("open pty: %w", err)
	}
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		_ = m.Close()
		return fmt.Errorf("unlock pty: %w", err)
	}
	s.pty, s.term = m, req.Term
	return setWinsize(m, payload[4+len(req.Term):])
}

// mountDevpts mounts devpts at /dev/pts once: the ramdisk mounts none, and
// /dev/ptmx needs it. A devpts that is already there is used.
var mountDevpts = sync.OnceValue(func() error {
	var st unix.Statfs_t
	if unix.Statfs("/dev/pts", &st) == nil && st.Type == unix.DEVPTS_SUPER_MAGIC {
		return nil
	}
	_ = os.MkdirAll("/dev/pts", 0o755)
	if err := unix.Mount("devpts", "/dev/pts", "devpts", unix.MS_NOSUID|unix.MS_NOEXEC, "mode=0620,ptmxmode=0666"); err != nil {
		return fmt.Errorf("mount devpts: %w", err)
	}
	return nil
})

// setWinsize applies a window-change (cols, rows, px width, px height).
func setWinsize(pty *os.File, payload []byte) error {
	if len(payload) < 8 {
		return errors.New("short window-change")
	}
	cols, rows := binary.BigEndian.Uint32(payload), binary.BigEndian.Uint32(payload[4:])
	ws := &unix.Winsize{Row: uint16(rows), Col: uint16(cols)} //nolint:gosec // terminal sizes fit
	if err := unix.IoctlSetWinsize(int(pty.Fd()), unix.TIOCSWINSZ, ws); err != nil {
		return fmt.Errorf("set window size: %w", err)
	}
	return nil
}

// start runs the login shell or, for exec, "sh -c command", and closes the
// channel with its exit status.
func (s *session) start(req *ssh.Request) error {
	args := []string{rescueShell}
	if req.Type == "exec" {
		var cmd struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &cmd); err != nil {
			return fmt.Errorf("exec request: %w", err)
		}
		args = append(args, "-c", cmd.Command)
	}
	cmd := exec.CommandContext(s.ctx, args[0], args[1:]...)
	cmd.Dir = "/"
	if unix.Access("/root", unix.X_OK) == nil {
		cmd.Dir = "/root"
	}
	term := s.term
	if term == "" {
		term = "dumb"
	}
	cmd.Env = []string{"HOME=" + cmd.Dir, "USER=root", "LOGNAME=root", "SHELL=" + rescueShell, "TERM=" + term,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	var done func()
	if s.pty != nil {
		var err error
		if done, err = s.attachPTY(cmd); err != nil {
			return err
		}
	} else {
		in, err := cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("stdin: %w", err)
		}
		cmd.Stdout, cmd.Stderr = s.ch, s.ch.Stderr()
		go func() { _, _ = io.Copy(in, s.ch); _ = in.Close() }()
		done = func() {}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	s.started = true
	go func() {
		err := cmd.Wait()
		done()
		code := 0
		if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			code = 255
		}
		_, _ = s.ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(code)})) //nolint:gosec // exit codes are small
		_ = s.ch.Close()
	}()
	return nil
}

// attachPTY gives cmd the terminal as its controlling terminal and copies
// between the terminal and the channel. The returned func waits for the
// output after the command ended.
func (s *session) attachPTY(cmd *exec.Cmd) (func(), error) {
	n, err := unix.IoctlGetInt(int(s.pty.Fd()), unix.TIOCGPTN)
	if err != nil {
		return nil, fmt.Errorf("pty number: %w", err)
	}
	tty, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open tty: %w", err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	out := make(chan struct{})
	go func() { _, _ = io.Copy(s.pty, s.ch) }()
	go func() { _, _ = io.Copy(s.ch, s.pty); close(out) }()
	return func() {
		// The terminal reads EIO once the last holder of the tty side closed it.
		_ = tty.Close()
		select {
		case <-out:
		case <-time.After(2 * time.Second):
		}
	}, nil
}
