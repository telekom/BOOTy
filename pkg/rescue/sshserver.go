//go:build linux

package rescue

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// The rescue SSH server. It replaces dropbear with the same behavior: see
// docs/configuration.md#rescue-ssh-server for the parity list.
const (
	rescueUser = "root"
	// sshAuthTimeout, sshMaxAuthTries, sshMaxUnauthPerIP and
	// sshMaxUnauth are dropbear's AUTH_TIMEOUT, MAX_AUTH_TRIES,
	// MAX_UNAUTH_PER_IP and MAX_UNAUTH_CLIENTS.
	sshAuthTimeout    = 300 * time.Second
	sshMaxAuthTries   = 10
	sshMaxUnauthPerIP = 5
	sshMaxUnauth      = 30
	sshIdle           = 900 * time.Second // dropbear -I 900
	sshVerifyTimeout  = 30 * time.Second
)

// sshListenAddr is a variable for tests.
var sshListenAddr = ":22"

// The algorithms of the hardened dropbear build (hack/dropbear-localoptions.h
// before the switch), and the ones RESCUE_SSH_HARDENED=false adds, which an
// upstream dropbear build had: RSA keys, group14 and HMAC-SHA1.
var (
	sshKex = []string{ssh.KeyExchangeMLKEM768X25519, ssh.KeyExchangeCurve25519, "curve25519-sha256@libssh.org",
		ssh.KeyExchangeECDHP256, ssh.KeyExchangeECDHP384, ssh.KeyExchangeECDHP521}
	sshCiphers  = []string{ssh.CipherChaCha20Poly1305, ssh.CipherAES128CTR, ssh.CipherAES256CTR}
	sshMACs     = []string{ssh.HMACSHA256}
	sshUserAlgs = []string{ssh.KeyAlgoED25519, ssh.KeyAlgoSKED25519,
		ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoSKECDSA256}

	legacyKex      = []string{ssh.KeyExchangeDH14SHA256, ssh.InsecureKeyExchangeDH14SHA1}
	legacyMACs     = []string{ssh.HMACSHA1}
	legacyUserAlgs = []string{ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSA}
)

// certVerifier checks an opkssh certificate for a login as user and returns
// the issuer and subject of its PK Token (unverified when err is set).
type certVerifier func(ctx context.Context, user string, cert *ssh.Certificate) (issuer, subject string, err error)

// rescueSSH is the configured server.
type rescueSSH struct {
	ctx      context.Context
	legacy   bool
	keys     []authorizedKey
	password string // crypt hash; empty: no password login
	opk      certVerifier
	unauthMu sync.Mutex
	unauth   map[string]int
}

// startRescueSSH serves rescue SSH on port 22 until ctx ends.
func startRescueSSH(ctx context.Context, cfg *Config) error {
	s := newRescueSSH(ctx, cfg)
	srv, fingerprint, err := s.serverConfig()
	if err != nil {
		return err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", sshListenAddr)
	if err != nil {
		return fmt.Errorf("rescue SSH listen: %w", err)
	}
	go func() { <-ctx.Done(); _ = ln.Close() }()
	go s.serve(ln, srv)
	slog.Info("rescue SSH server started", "hardened", !s.legacy, "keys", len(s.keys), "password", s.password != "",
		"opkssh", s.opk != nil, "host_key", fingerprint)
	fmt.Println("SSH daemon started on port 22") //nolint:forbidigo // rescue mode output
	return nil
}

// newRescueSSH applies the login rules of the dropbear setup: key-only once
// a key entry (or an opkssh setting) is set, password login only without,
// both with RESCUE_SSH_HARDENED=false.
func newRescueSSH(ctx context.Context, cfg *Config) *rescueSSH {
	s := &rescueSSH{ctx: ctx, legacy: cfg.LegacySSH, unauth: map[string]int{}}
	for i, e := range cfg.SSHKeys {
		k, err := parseAuthorizedKey(e)
		if err != nil {
			slog.Warn("ignoring rescue SSH key with unsupported options", "entry", i+1, "error", err)
			continue
		}
		s.keys = append(s.keys, k)
	}
	opkSet := cfg.OPKProviders != "" || cfg.OPKPolicy != ""
	if opkSet {
		v, err := newCertVerifier(cfg)
		switch {
		case errors.Is(err, errOPKUnsupported):
			slog.Warn("ignoring the opkssh settings", "error", err)
		case err != nil:
			slog.Warn("rescue SSH accepts no opkssh login", "error", err)
		default:
			s.opk = v
		}
	}
	keyOnly := len(cfg.SSHKeys) > 0 || cfg.SSHKeyConfigured || opkSet
	switch {
	case cfg.LegacySSH:
		slog.Warn("rescue SSH hardening disabled (RESCUE_SSH_HARDENED=false): password login, RSA keys and port forwarding allowed, no idle timeout")
		s.password = cfg.PasswordHash
	case len(cfg.SSHKeys) == 0 && cfg.SSHKeyConfigured:
		slog.Warn("rescue SSH has no valid key in RESCUE_SSH_PUBKEY: SSH login is refused, also with a password hash")
	case !keyOnly && cfg.PasswordHash != "":
		slog.Warn("rescue SSH allows password login: a password hash is set and no SSH key")
		s.password = cfg.PasswordHash
	}
	return s
}

// serverConfig returns the server config with host keys generated for this
// boot (Ed25519, and RSA for RESCUE_SSH_HARDENED=false) and the SHA-256
// fingerprint of the Ed25519 key.
func (s *rescueSSH) serverConfig() (*ssh.ServerConfig, string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("rescue SSH host key: %w", err)
	}
	host, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, "", fmt.Errorf("rescue SSH host key: %w", err)
	}
	srv := &ssh.ServerConfig{
		Config:                  ssh.Config{KeyExchanges: sshKex, Ciphers: sshCiphers, MACs: sshMACs},
		PublicKeyCallback:       s.publicKey,
		PublicKeyAuthAlgorithms: sshUserAlgs,
		MaxAuthTries:            sshMaxAuthTries,
		ServerVersion:           "SSH-2.0-BOOTy",
	}
	srv.AddHostKey(host)
	if s.password != "" {
		srv.PasswordCallback = s.checkPassword
	}
	if s.legacy {
		srv.KeyExchanges = slices.Concat(sshKex, legacyKex)
		srv.MACs = slices.Concat(sshMACs, legacyMACs)
		srv.PublicKeyAuthAlgorithms = slices.Concat(sshUserAlgs, legacyUserAlgs)
		rsaKey, err := rsa.GenerateKey(rand.Reader, 3072)
		if err != nil {
			return nil, "", fmt.Errorf("rescue SSH RSA host key: %w", err)
		}
		rsaHost, err := ssh.NewSignerFromKey(rsaKey)
		if err != nil {
			return nil, "", fmt.Errorf("rescue SSH RSA host key: %w", err)
		}
		srv.AddHostKey(rsaHost)
	}
	return srv, ssh.FingerprintSHA256(host.PublicKey()), nil
}

// errLoginRefused is the error the SSH client gets; the reason is only in
// the log.
var errLoginRefused = errors.New("rescue SSH login refused")

// keyOptionsKey is the Permissions.ExtraData key of the *keyOptions of the
// login.
type keyOptionsKey struct{}

func refuse(meta ssh.ConnMetadata, method, reason string, attrs ...any) error {
	slog.Warn("rescue SSH login refused", append([]any{"method", method, "principal", meta.User(),
		"remote", meta.RemoteAddr().String(), "reason", reason}, attrs...)...)
	return errLoginRefused
}

// publicKey accepts a RESCUE_SSH_PUBKEY key or an opkssh certificate for
// root. The accepted login is logged after the handshake, when the client
// has proven that it holds the key.
func (s *rescueSSH) publicKey(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	if cert, ok := key.(*ssh.Certificate); ok && cert.Extensions["openpubkey-pkt"] != "" {
		if s.opk == nil {
			return nil, refuse(meta, "opkssh", "no valid opkssh provider configured")
		}
		ctx, cancel := context.WithTimeout(s.ctx, sshVerifyTimeout)
		defer cancel()
		issuer, subject, err := s.opk(ctx, meta.User(), cert)
		if err != nil {
			return nil, refuse(meta, "opkssh", err.Error(), "issuer", issuer, "subject", subject)
		}
		return &ssh.Permissions{
			Extensions: map[string]string{"method": "opkssh", "issuer": issuer, "subject": subject},
			ExtraData:  map[any]any{keyOptionsKey{}: &keyOptions{}},
		}, nil
	}
	fp := ssh.FingerprintSHA256(key)
	if meta.User() != rescueUser {
		return nil, refuse(meta, "publickey", "only "+rescueUser+" can log in", "key", fp)
	}
	wire := key.Marshal()
	i := slices.IndexFunc(s.keys, func(k authorizedKey) bool { return bytes.Equal(k.key.Marshal(), wire) })
	if i < 0 {
		return nil, refuse(meta, "publickey", "not a RESCUE_SSH_PUBKEY key", "key", fp)
	}
	opts := s.keys[i].opts
	p := &ssh.Permissions{
		Extensions: map[string]string{"method": "publickey", "key": fp},
		ExtraData:  map[any]any{keyOptionsKey{}: &opts},
	}
	if opts.noTouchRequired {
		p.Extensions["no-touch-required"] = ""
	}
	return p, nil
}

// checkPassword checks the password against RESCUE_PASSWORD_HASH.
func (s *rescueSSH) checkPassword(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	if meta.User() != rescueUser {
		return nil, refuse(meta, "password", "only "+rescueUser+" can log in")
	}
	if !checkPassword(s.password, string(password)) {
		return nil, refuse(meta, "password", "wrong password")
	}
	return &ssh.Permissions{
		Extensions: map[string]string{"method": "password"},
		ExtraData:  map[any]any{keyOptionsKey{}: &keyOptions{}},
	}, nil
}

// serve accepts connections until ln is closed.
func (s *rescueSSH) serve(ln net.Listener, srv *ssh.ServerConfig) {
	for {
		nc, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				slog.Warn("rescue SSH server stopped", "error", err)
			}
			return
		}
		go s.serveConn(nc, srv)
	}
}

// admit counts an unauthenticated connection against dropbear's limits and
// returns its release func, or nil when a limit is reached.
func (s *rescueSSH) admit(nc net.Conn) func() {
	ip, _, _ := net.SplitHostPort(nc.RemoteAddr().String())
	s.unauthMu.Lock()
	defer s.unauthMu.Unlock()
	total := 0
	for _, n := range s.unauth {
		total += n
	}
	if total >= sshMaxUnauth || s.unauth[ip] >= sshMaxUnauthPerIP {
		return nil
	}
	s.unauth[ip]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.unauthMu.Lock()
			defer s.unauthMu.Unlock()
			s.unauth[ip]--
			if s.unauth[ip] == 0 {
				delete(s.unauth, ip)
			}
		})
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

func (s *rescueSSH) serveConn(nc net.Conn, srv *ssh.ServerConfig) {
	release := s.admit(nc)
	if release == nil {
		slog.Warn("rescue SSH connection refused: too many connections without a login", "remote", nc.RemoteAddr().String())
		_ = nc.Close()
		return
	}
	ic := &idleConn{Conn: nc}
	_ = nc.SetDeadline(time.Now().Add(sshAuthTimeout))
	conn, chans, reqs, err := ssh.NewServerConn(ic, srv)
	release()
	if err != nil {
		slog.Debug("rescue SSH connection closed before a login", "remote", nc.RemoteAddr().String(), "error", err)
		_ = nc.Close()
		return
	}
	_ = nc.SetDeadline(time.Time{})
	if !s.legacy {
		ic.timeout.Store(int64(sshIdle))
		ic.extend()
	}
	defer func() { _ = conn.Close() }()
	attrs := []any{"principal", conn.User(), "remote", conn.RemoteAddr().String()}
	for _, k := range []string{"method", "issuer", "subject", "key"} {
		if v := conn.Permissions.Extensions[k]; v != "" {
			attrs = append(attrs, k, v)
		}
	}
	slog.Info("rescue SSH login accepted", attrs...)
	c := &rescueConn{ctx: s.ctx, conn: conn, legacy: s.legacy,
		opts: conn.Permissions.ExtraData[keyOptionsKey{}].(*keyOptions)} //nolint:forcetypeassert // every callback sets it
	defer c.closeForwards()
	go c.globalRequests(reqs)
	for nch := range chans {
		switch nch.ChannelType() {
		case "session":
			ch, creqs, err := nch.Accept()
			if err == nil {
				go c.serveSession(ch, creqs)
			}
		case "direct-tcpip":
			c.directTCPIP(nch)
		default:
			_ = nch.Reject(ssh.Prohibited, "channel type not allowed")
		}
	}
}

// rescueConn is a logged-in connection.
type rescueConn struct {
	ctx    context.Context
	conn   *ssh.ServerConn
	legacy bool
	opts   *keyOptions

	fwdMu sync.Mutex
	fwds  map[string]net.Listener
}
