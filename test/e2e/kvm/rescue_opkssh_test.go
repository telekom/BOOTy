//go:build e2e

package kvm

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/telekom/BOOTy/pkg/rescue/opktest"
)

// TestRescueSSHOPKLoginQEMU boots the default flavor with the opkssh
// settings: BOOTy serves rescue SSH itself. A mock OpenID provider on the
// host (HTTPS with a private CA, CA_BUNDLE_FILE) serves the JWKS. A PK Token
// key of an identity in the policy logs in, an expired one is refused, and
// a static RESCUE_SSH_PUBKEY key still works, with the hardening of the
// dropbear setup.
//
// BOOTY_FLAVOR_INITRAMFS: initramfs.cpio.zst of the default flavor.
// BOOTY_FLAVOR_KERNEL: vmlinuz of `--target kernel-image`.
func TestRescueSSHOPKLoginQEMU(t *testing.T) {
	qemuAvailable(t)
	flavor := os.Getenv("BOOTY_FLAVOR_INITRAMFS")
	kernel := os.Getenv("BOOTY_FLAVOR_KERNEL")
	if flavor == "" || kernel == "" {
		failOrSkipUnsupportedHost(t, "BOOTY_FLAVOR_INITRAMFS and BOOTY_FLAVOR_KERNEL are required")
	}
	requireKVMAssets(t, flavor, kernel)
	for _, tool := range []string{"ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			failOrSkipUnsupportedHost(t, "%s not available", tool)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	dir := t.TempDir()

	ca := newPrivateCA(t)
	prov := startMockOIDCProvider(t, ca)
	valid := writeOPKKey(ctx, t, prov, filepath.Join(dir, "id_opk"), map[string]any{"email": "alice@example.com"})
	// Issued 13 hours ago: older than the 12h expiration policy.
	expired := writeOPKKey(ctx, t, prov, filepath.Join(dir, "id_expired"),
		map[string]any{"email": "alice@example.com", "iat": time.Now().Add(-13 * time.Hour).Unix()})
	outsider := writeOPKKey(ctx, t, prov, filepath.Join(dir, "id_outsider"), map[string]any{"email": "mallory@example.com"})
	static := filepath.Join(dir, "id_static")
	if out, err := exec.CommandContext(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", static).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	staticPub, err := os.ReadFile(static + ".pub")
	if err != nil {
		t.Fatal(err)
	}

	image := filepath.Join(dir, "unused.img")
	writeFile(t, image, "")
	caprf := startFakeCAPRF(t, image)
	vars := caprf.nativeVars("provision")
	vars["PROVISION_TARGET_OS"] = "linux"
	vars["IMAGE"] = "http://10.0.2.2:1/missing.raw" // nothing listens: the provision fails
	vars["IMAGE_ALLOW_INSECURE_HTTP"] = "true"
	vars["RESCUE_MODE"] = "shell"
	vars["CA_BUNDLE_FILE"] = "/deploy/ca-bundle.pem"
	vars["RESCUE_SSH_PUBKEY"] = strings.TrimSpace(string(staticPub))
	vars["RESCUE_SSH_OPK_PROVIDERS"] = prov.Issuer + " " + prov.ClientID + " 12h"
	vars["RESCUE_SSH_OPK_POLICY"] = "root alice@example.com " + prov.Issuer
	initramfs := flavorInitramfsWithVars(t, flavor, vars, map[string]string{"deploy/ca-bundle.pem": ca.pem})

	port, console := bootRescueQEMU(ctx, t, kernel, initramfs)
	opkLogin := func(key string) ([]byte, error) {
		return rescueSSH(ctx, port, "-i", key, "-o", "CertificateFile="+key+"-cert.pub", "-o", "IdentitiesOnly=yes",
			"root@127.0.0.1", "echo rescue-ok; id -un")
	}
	var out []byte
	for range 30 {
		if out, err = opkLogin(valid); err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil || !strings.Contains(string(out), "rescue-ok") || !strings.Contains(string(out), "root") {
		t.Fatalf("opkssh login failed: %v\n%s\nconsole tail:\n%s", err, out, tail([]byte(console.String()), 3000))
	}
	for name, key := range map[string]string{"expired PK Token": expired, "identity without a policy row": outsider} {
		if out, err := opkLogin(key); err == nil || !strings.Contains(string(out), "Permission denied") {
			t.Errorf("%s: want a refusal, got %v\n%s", name, err, out)
		}
	}
	if out, err := rescueSSH(ctx, port, "-i", static, "-o", "IdentitiesOnly=yes", "root@127.0.0.1", "echo rescue-ok"); err != nil ||
		!strings.Contains(string(out), "rescue-ok") {
		t.Errorf("static key login failed: %v\n%s", err, out)
	}
	assertRescueSSHRefusals(ctx, t, port, static)

	log := console.String()
	for _, want := range []string{"rescue SSH login accepted", "method=opkssh", "rescue SSH login refused", "expired", "no RESCUE_SSH_OPK_POLICY row"} {
		if !strings.Contains(log, want) {
			t.Errorf("console has no %q; tail:\n%s", want, tail([]byte(log), 3000))
		}
	}
}

// startMockOIDCProvider serves a mock OpenID provider at
// https://10.0.2.2:<port> with a certificate of ca.
func startMockOIDCProvider(t *testing.T, ca privateCA) *opktest.Provider {
	t.Helper()
	var lc net.ListenConfig
	listener, err := lc.Listen(context.Background(), "tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	prov, err := opktest.NewProvider(fmt.Sprintf("https://10.0.2.2:%d", listener.Addr().(*net.TCPAddr).Port), "booty-rescue")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler:           prov,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{ca.server}},
	}
	go func() { _ = srv.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	return prov
}

// writeOPKKey writes an opkssh login key, as `opkssh login` does: the
// private key at path and the certificate with the PK Token at
// path-cert.pub.
func writeOPKKey(ctx context.Context, t *testing.T, prov *opktest.Provider, path string, claims map[string]any) string {
	t.Helper()
	key, err := prov.NewKey(ctx, claims)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key.Private, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path+"-cert.pub", string(ssh.MarshalAuthorizedKey(key.Cert)))
	return path
}

// syncBuilder is a strings.Builder for the console that a goroutine fills.
type syncBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuilder) add(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.WriteString(line + "\n")
}

func (s *syncBuilder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// bootRescueQEMU boots the initramfs with port 22 forwarded to a free host
// port and waits for the rescue SSH server. The console keeps recording.
func bootRescueQEMU(ctx context.Context, t *testing.T, kernel, initramfs string) (int, *syncBuilder) {
	t.Helper()
	port := freeTCPPort(ctx, t)
	args := append([]string{
		"-m", "2048", "-nographic", "-no-reboot",
		"-kernel", kernel, "-initrd", initramfs,
		"-net", "nic,model=virtio,macaddr=52:54:00:12:34:56",
		"-net", fmt.Sprintf("user,hostfwd=tcp:127.0.0.1:%d-:22", port),
		"-append", "console=ttyS0 panic=1 net.ifnames=0",
	}, splitExtraArgs(os.Getenv("QEMU_EXTRA_ARGS"))...)
	qemu := exec.CommandContext(ctx, "qemu-system-x86_64", args...)
	pipe, err := qemu.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	qemu.Stderr = qemu.Stdout
	if err := qemu.Start(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(func() { cancel(); _ = qemu.Wait() })
	console := &syncBuilder{}
	started, ended := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(ended)
		sc := bufio.NewScanner(pipe)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		once := sync.Once{}
		for sc.Scan() {
			console.add(sc.Text())
			if strings.Contains(sc.Text(), "SSH daemon started on port 22") {
				once.Do(func() { close(started) })
			}
		}
	}()
	select {
	case <-started:
	case <-ended:
		t.Fatalf("QEMU ended without a rescue SSH server; tail:\n%s", tail([]byte(console.String()), 3000))
	case <-ctx.Done():
		t.Fatalf("no rescue SSH server; tail:\n%s", tail([]byte(console.String()), 3000))
	}
	return port, console
}
