// Package rescue provides rescue mode for failed provisioning recovery.
package rescue

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	exec "github.com/telekom/BOOTy/pkg/executil"
)

var (
	rootSSHDir = "/root/.ssh"
	shadowPath = "/etc/shadow"
)

var supportedPasswordHashPrefixes = []string{
	"$1$",
	"$2a$",
	"$2b$",
	"$2y$",
	"$5$",
	"$6$",
	"$y$",
}

// Mode determines what happens when provisioning fails.
type Mode string

const (
	// ModeReboot reboots the machine after failure.
	ModeReboot Mode = "reboot"
	// ModeShell drops to a debug shell.
	ModeShell Mode = "shell"
	// ModeRetry retries the provisioning.
	ModeRetry Mode = "retry"
	// ModeWait waits for manual intervention.
	ModeWait Mode = "wait"
)

// ParseMode parses a rescue mode string.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModeReboot, ModeShell, ModeRetry, ModeWait:
		return Mode(s), nil
	default:
		return "", fmt.Errorf("unknown rescue mode %q", s)
	}
}

// Config holds rescue mode configuration.
type Config struct {
	Mode           Mode          `json:"mode"`
	MaxRetries     int           `json:"maxRetries,omitempty"`
	RetryDelay     time.Duration `json:"retryDelay,omitempty"`
	ShellTimeout   time.Duration `json:"shellTimeout,omitempty"`
	SSHKeys        []string      `json:"sshKeys,omitempty"`
	PasswordHash   string        `json:"passwordHash,omitempty"`
	AutoMountDisks bool          `json:"autoMountDisks,omitempty"`
	NetworkConfig  bool          `json:"networkConfig,omitempty"`
	// LegacySSH starts dropbear without the hardening flags (password
	// login, port forwarding, no idle timeout): RESCUE_SSH_HARDENED=false.
	LegacySSH bool `json:"legacySSH,omitempty"`
	// SSHKeyConfigured is set when RESCUE_SSH_PUBKEY has an entry, valid or
	// not: login then stays key-only, so a malformed key never turns the
	// password login on.
	SSHKeyConfigured bool `json:"sshKeyConfigured,omitempty"`
}

// Validate checks the rescue config.
func (c *Config) Validate() error {
	if c.Mode != "" {
		if _, err := ParseMode(string(c.Mode)); err != nil {
			return fmt.Errorf("invalid mode: %w", err)
		}
	}
	if c.Mode == ModeRetry && c.MaxRetries < 1 {
		return fmt.Errorf("maxRetries must be >= 1 for retry mode")
	}
	if c.RetryDelay < 0 {
		return fmt.Errorf("retryDelay must be non-negative")
	}
	if c.ShellTimeout < 0 {
		return fmt.Errorf("shellTimeout must be non-negative")
	}
	return nil
}

// ApplyDefaults sets default values for unset fields.
func (c *Config) ApplyDefaults() {
	if c.Mode == "" {
		c.Mode = ModeReboot
	}
	if c.MaxRetries == 0 && c.Mode == ModeRetry {
		c.MaxRetries = 3
	}
	if c.RetryDelay == 0 {
		c.RetryDelay = 30 * time.Second
	}
	if c.ShellTimeout == 0 {
		c.ShellTimeout = 30 * time.Minute
	}
}

// Action represents a rescue action to take.
type Action struct {
	Type    Mode   `json:"type"`
	Message string `json:"message"`
}

// RetryState tracks retry attempts.
type RetryState struct {
	Attempts   int       `json:"attempts"`
	MaxRetries int       `json:"maxRetries"`
	LastError  string    `json:"lastError,omitempty"`
	LastRetry  time.Time `json:"lastRetry,omitempty"`
}

// CanRetry returns whether another retry is allowed.
func (s *RetryState) CanRetry() bool {
	return s.Attempts < s.MaxRetries
}

// RecordAttempt records a retry attempt.
func (s *RetryState) RecordAttempt(err error) {
	s.Attempts++
	s.LastRetry = time.Now()
	if err != nil {
		s.LastError = err.Error()
		return
	}
	s.LastError = ""
}

// Remaining returns the number of retries remaining.
func (s *RetryState) Remaining() int {
	r := s.MaxRetries - s.Attempts
	if r < 0 {
		return 0
	}
	return r
}

// Decide determines the rescue action based on config and state.
func Decide(cfg *Config, state *RetryState) Action {
	if state == nil {
		state = &RetryState{}
	}
	if cfg.MaxRetries > 0 {
		state.MaxRetries = cfg.MaxRetries
	}

	switch cfg.Mode {
	case ModeRetry:
		if state.CanRetry() {
			return Action{
				Type:    ModeRetry,
				Message: fmt.Sprintf("retrying (%d/%d)", state.Attempts+1, state.MaxRetries),
			}
		}
		return Action{
			Type:    ModeReboot,
			Message: "max retries exceeded, rebooting",
		}
	case ModeShell:
		return Action{
			Type:    ModeShell,
			Message: "dropping to rescue shell",
		}
	case ModeWait:
		return Action{
			Type:    ModeWait,
			Message: "waiting for manual intervention",
		}
	default:
		return Action{
			Type:    ModeReboot,
			Message: "rebooting",
		}
	}
}

// Setup prepares the rescue environment based on the config.
// It configures SSH access and password authentication when rescue
// mode is shell or wait.
func Setup(ctx context.Context, cfg *Config) error {
	if cfg.Mode != ModeShell && cfg.Mode != ModeWait {
		return nil
	}

	if err := setupSSHKeys(cfg.SSHKeys); err != nil {
		return fmt.Errorf("setting up SSH keys: %w", err)
	}

	if cfg.PasswordHash != "" {
		if err := setupPasswordHash(cfg.PasswordHash); err != nil {
			return fmt.Errorf("setting up password: %w", err)
		}
	}

	if err := startDropbear(ctx, cfg); err != nil {
		slog.Warn("could not start SSH daemon", "error", err)
	}

	if cfg.AutoMountDisks {
		if err := autoMountDisks(ctx); err != nil {
			slog.Warn("auto-mount failed", "error", err)
		}
	}

	return nil
}

// setupSSHKeys writes authorized keys for root. Without keys it removes an
// authorized_keys file that something else put into the ramdisk (a deploy
// archive with PROVISIONER_FILES_TARGET=ramdisk), so only the trusted
// configuration decides who may log in.
func setupSSHKeys(keys []string) error {
	if len(keys) == 0 {
		err := os.Remove(filepath.Join(rootSSHDir, "authorized_keys"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing stale authorized_keys: %w", err)
		}
		return nil
	}

	if err := os.MkdirAll(rootSSHDir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", rootSSHDir, err)
	}

	content := ""
	for _, k := range keys {
		content += k + "\n"
	}

	if err := os.WriteFile(filepath.Join(rootSSHDir, "authorized_keys"), []byte(content), 0o600); err != nil {
		return fmt.Errorf("writing authorized_keys: %w", err)
	}
	return nil
}

// setupPasswordHash sets the root password hash in /etc/shadow.
func setupPasswordHash(hash string) error {
	if err := validatePasswordHash(hash); err != nil {
		return err
	}

	shadow, err := os.ReadFile(shadowPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading shadow: %w", err)
		}
		entry := fmt.Sprintf("root:%s:19000:0:99999:7:::\n", hash)
		if wErr := os.WriteFile(shadowPath, []byte(entry), 0o600); wErr != nil { //nolint:gosec // G703: fixed rescue-mode shadow path, no user-controlled path
			return fmt.Errorf("writing shadow: %w", wErr)
		}
		return nil
	}

	lines := strings.Split(string(shadow), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, "root:") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) >= 3 {
				lines[i] = "root:" + hash + ":" + parts[2]
			}
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, fmt.Sprintf("root:%s:19000:0:99999:7:::", hash))
	}

	serialized := strings.Join(lines, "\n")
	if !strings.HasSuffix(serialized, "\n") {
		serialized += "\n"
	}
	if err := os.WriteFile(shadowPath, []byte(serialized), 0o600); err != nil { //nolint:gosec // G703: fixed rescue-mode shadow path, no user-controlled path
		return fmt.Errorf("writing shadow: %w", err)
	}
	return nil
}

func validatePasswordHash(hash string) error {
	if hash == "" {
		return fmt.Errorf("rescue password hash must not be empty")
	}
	if strings.ContainsAny(hash, ":\r\n") {
		return fmt.Errorf("invalid rescue password hash: contains forbidden shadow delimiters")
	}
	for _, prefix := range supportedPasswordHashPrefixes {
		if strings.HasPrefix(hash, prefix) {
			return nil
		}
	}
	return fmt.Errorf("invalid rescue password hash: unsupported Linux shadow hash prefix")
}

// Host keys, generated on every boot (the ramdisk starts empty). The RSA
// key exists only with RESCUE_SSH_HARDENED=false and a dropbear built with
// DROPBEAR_HARDENED=false.
const (
	hostKeyPath    = "/etc/dropbear/dropbear_ed25519_host_key"
	rsaHostKeyPath = "/etc/dropbear/dropbear_rsa_host_key"
)

// sshIdleTimeout closes rescue SSH sessions without traffic, in seconds.
const sshIdleTimeout = "900"

// dropbearArgs returns the dropbear command line. By default: no password
// login once a key is configured, no local or remote port forwarding, and
// an idle timeout. Password login stays only for a password hash without a
// key (not even a malformed one), so such setups keep a way in.
func dropbearArgs(cfg *Config, hostKeys []string) []string {
	var args []string
	for _, k := range hostKeys {
		args = append(args, "-r", k)
	}
	if !cfg.LegacySSH {
		if len(cfg.SSHKeys) > 0 || cfg.SSHKeyConfigured || cfg.PasswordHash == "" {
			args = append(args, "-s")
		}
		args = append(args, "-j", "-k", "-I", sshIdleTimeout)
	}
	return append(args, "-F", "-E", "-p", "22")
}

// startDropbear starts the dropbear SSH daemon if available.
func startDropbear(ctx context.Context, cfg *Config) error {
	path, err := exec.LookPath("dropbear")
	if err != nil {
		return fmt.Errorf("dropbear not found: %w", err)
	}

	hostKeys, err := generateHostKeys(ctx, cfg)
	if err != nil {
		return err
	}

	switch {
	case cfg.LegacySSH:
		slog.Warn("rescue SSH hardening disabled (RESCUE_SSH_HARDENED=false): password login and port forwarding allowed, no idle timeout")
	case len(cfg.SSHKeys) == 0 && cfg.SSHKeyConfigured:
		slog.Warn("rescue SSH has no valid key in RESCUE_SSH_PUBKEY: SSH login is refused, also with a password hash")
	case len(cfg.SSHKeys) == 0 && cfg.PasswordHash != "":
		slog.Warn("rescue SSH allows password login: a password hash is set and no SSH key")
	}

	cmd := exec.CommandContext(ctx, path, dropbearArgs(cfg, hostKeys)...) //nolint:gosec // fixed args
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting dropbear: %w", err)
	}

	// Reap the child process in the background to avoid zombies (BOOTy is PID 1).
	go func() { _ = cmd.Wait() }()

	fmt.Printf("SSH daemon started on port 22 (PID %d)\n", cmd.Process.Pid) //nolint:forbidigo // rescue mode output
	return nil
}

// generateHostKeys creates the Ed25519 host key and, for RESCUE_SSH_HARDENED
// =false, an RSA host key when the dropbear build supports RSA (for old
// clients), and returns the key files to load.
func generateHostKeys(ctx context.Context, cfg *Config) ([]string, error) {
	keygenPath, err := exec.LookPath("dropbearkey")
	if err != nil {
		return nil, fmt.Errorf("dropbearkey not found: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(hostKeyPath), 0o700); err != nil {
		return nil, fmt.Errorf("creating dropbear dir: %w", err)
	}
	keygen := func(keyType, path string) error {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		out, err := exec.CommandContext(ctx, keygenPath, "-t", keyType, "-f", path).CombinedOutput() //nolint:gosec // fixed args
		if err != nil {
			return fmt.Errorf("generating %s host key: %w\n%s", keyType, err, string(out))
		}
		return nil
	}
	if err := keygen("ed25519", hostKeyPath); err != nil {
		return nil, err
	}
	keys := []string{hostKeyPath}
	if cfg.LegacySSH {
		if err := keygen("rsa", rsaHostKeyPath); err != nil {
			slog.Info("no RSA host key: this dropbear build has no RSA support", "error", err)
		} else {
			keys = append(keys, rsaHostKeyPath)
		}
	}
	return keys, nil
}

// autoMountDisks discovers block devices and mounts them under /mnt/rescue/.
func autoMountDisks(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "lsblk", "-rnpo", "NAME,FSTYPE,MOUNTPOINT").CombinedOutput()
	if err != nil {
		return fmt.Errorf("listing block devices: %w", err)
	}

	mounted := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		dev := fields[0]
		fstype := fields[1]
		// Skip devices without a filesystem or already mounted.
		if fstype == "" || (len(fields) >= 3 && fields[2] != "") {
			continue
		}

		mountpoint := fmt.Sprintf("/mnt/rescue/%s", filepath.Base(dev))
		if err := os.MkdirAll(mountpoint, 0o755); err != nil {
			fmt.Printf("warning: cannot create %s: %v\n", mountpoint, err) //nolint:forbidigo // rescue mode output
			continue
		}

		cmd := exec.CommandContext(ctx, "mount", "-o", "ro", dev, mountpoint)
		if mErr := cmd.Run(); mErr != nil {
			fmt.Printf("warning: mount %s → %s failed: %v\n", dev, mountpoint, mErr) //nolint:forbidigo // rescue mode output
			continue
		}
		fmt.Printf("Mounted %s (%s) → %s\n", dev, fstype, mountpoint) //nolint:forbidigo // rescue mode output
		mounted++
	}

	fmt.Printf("Auto-mounted %d disk(s) under /mnt/rescue/\n", mounted) //nolint:forbidigo // rescue mode output
	return nil
}
