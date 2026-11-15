//go:build e2e

package kvm

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRescueSSHLoginQEMU boots the default flavor (its dropbear and its
// /etc/passwd), lets the provision fail on an unreachable image, and logs
// in to the rescue shell over SSH with each of two RESCUE_SSH_PUBKEY keys.
// Without a root entry in /etc/passwd, dropbear refused every login
// ("nonexistent user"). The hardened server must refuse password login
// (although RESCUE_PASSWORD_HASH is set), an RSA host key, group14 key
// exchange, TCP forwarding and the SFTP subsystem.
//
// BOOTY_FLAVOR_INITRAMFS: initramfs.cpio.zst of the default flavor.
// BOOTY_FLAVOR_KERNEL: vmlinuz of `--target kernel-image`.
func TestRescueSSHLoginQEMU(t *testing.T) {
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
	keys := []string{filepath.Join(dir, "id_a"), filepath.Join(dir, "id_b")}
	var pubs []string
	for _, key := range keys {
		if out, err := exec.CommandContext(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v\n%s", err, out)
		}
		pub, err := os.ReadFile(key + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		pubs = append(pubs, strings.TrimSpace(string(pub)))
	}

	image := filepath.Join(dir, "unused.img")
	writeFile(t, image, "")
	caprf := startFakeCAPRF(t, image)
	vars := caprf.nativeVars("provision")
	vars["PROVISION_TARGET_OS"] = "linux"
	vars["IMAGE"] = "http://10.0.2.2:1/missing.raw" // nothing listens: the provision fails
	vars["IMAGE_ALLOW_INSECURE_HTTP"] = "true"
	vars["RESCUE_MODE"] = "shell"
	// Two keys, an empty and a malformed entry, which BOOTy skips.
	vars["RESCUE_SSH_PUBKEY"] = pubs[0] + ";not-a-key;;" + pubs[1]
	vars["RESCUE_PASSWORD_HASH"] = "$6$rescue$4CGyX0OaQIM6Uu1TqF1C4f8V5jyDT9N2ITqE6wA3lFQmB6zqh5lW5qOHkUzQhC6RJgWbG6V0X4i7vYpJ3m0iA/"
	initramfs := flavorInitramfsWithVars(t, flavor, vars, nil)

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
	t.Cleanup(func() { cancel(); _ = qemu.Wait() })

	var serial strings.Builder
	started := false
	sc := bufio.NewScanner(pipe)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		serial.WriteString(sc.Text() + "\n")
		if strings.Contains(sc.Text(), "SSH daemon started on port 22") {
			started = true
			break
		}
	}
	if !started {
		t.Fatalf("no rescue SSH daemon; tail:\n%s", tail([]byte(serial.String()), 3000))
	}
	go func() { // keep draining the console
		for sc.Scan() {
		}
	}()

	login := func(key string) ([]byte, error) {
		return rescueSSH(ctx, port, "-i", key, "-o", "HostKeyAlgorithms=ssh-ed25519",
			"root@127.0.0.1", "echo rescue-ok; id -un")
	}
	var out []byte
	for range 30 {
		out, err = login(keys[0])
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	for i, key := range keys {
		if i > 0 {
			out, err = login(key)
		}
		if err != nil || !strings.Contains(string(out), "rescue-ok") || !strings.Contains(string(out), "root") {
			t.Fatalf("rescue SSH login with key %d failed: %v\n%s\nconsole tail:\n%s", i+1, err, out, tail([]byte(serial.String()), 2000))
		}
	}
	assertRescueSSHRefusals(ctx, t, port, keys[0])
}

// assertRescueSSHRefusals checks what the hardened rescue SSH server
// refuses. ssh -W opens a direct-tcpip channel, the channel of -L.
func assertRescueSSHRefusals(ctx context.Context, t *testing.T, port int, key string) {
	t.Helper()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"password login", []string{"-o", "PubkeyAuthentication=no", "-o", "PreferredAuthentications=password,keyboard-interactive",
			"root@127.0.0.1", "true"}, "Permission denied (publickey)"},
		{"RSA host key", []string{"-i", key, "-o", "HostKeyAlgorithms=rsa-sha2-512,rsa-sha2-256,ssh-rsa",
			"root@127.0.0.1", "true"}, "no matching host key type"},
		{"group14 key exchange", []string{"-i", key, "-o", "KexAlgorithms=diffie-hellman-group14-sha256",
			"root@127.0.0.1", "true"}, "no matching key exchange method"},
		{"local forwarding", []string{"-i", key, "-W", "127.0.0.1:22", "root@127.0.0.1"}, "administratively prohibited"},
		{"SFTP subsystem", []string{"-i", key, "-s", "root@127.0.0.1", "sftp"}, "subsystem request failed"},
	} {
		out, _ := rescueSSH(ctx, port, tc.args...)
		if !strings.Contains(string(out), tc.want) {
			t.Errorf("%s: want %q in the ssh output, got:\n%s", tc.name, tc.want, out)
		}
	}
}

// rescueSSH runs ssh against the forwarded rescue SSH port, non-interactive.
func rescueSSH(ctx context.Context, port int, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ssh", append([]string{"-p", fmt.Sprint(port),
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}, args...)...)
	return cmd.CombinedOutput()
}

// freeTCPPort returns a free TCP port on 127.0.0.1.
func freeTCPPort(ctx context.Context, t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}
