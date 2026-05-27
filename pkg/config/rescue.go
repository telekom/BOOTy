package config

// RescueConfig controls behavior when provisioning fails and the machine
// enters rescue mode. Shared across provision and deprovision modes.
type RescueConfig struct {
	// Mode determines what happens after a provisioning failure.
	// Valid values: "reboot" (default), "retry", "shell", "wait"
	//   - reboot: immediate reboot
	//   - retry: retry provisioning with backoff
	//   - shell: drop to interactive rescue shell (requires SSH key or password)
	//   - wait: wait indefinitely for manual intervention
	// Default: "reboot"
	Mode string `yaml:"mode" json:"mode"`

	// SSHPubKey is the authorized SSH public key for rescue shell access.
	// Example: "ssh-ed25519 AAAAC3... admin@ops"
	// Default: ""
	SSHPubKey string `yaml:"sshPubKey" json:"sshPubKey"`

	// PasswordHash is the crypt(3) password hash for rescue shell login.
	// Default: ""
	PasswordHash string `yaml:"passwordHash" json:"passwordHash"`

	// Timeout is seconds before the rescue auto-action fires.
	// 0 means wait indefinitely (for "shell" and "wait" modes).
	// Default: 0
	Timeout int `yaml:"timeout" json:"timeout"`

	// AutoMountDisks enables automatic mounting of discovered disks in rescue mode.
	// Useful for inspecting the installed OS from the rescue shell.
	// Default: false
	AutoMountDisks bool `yaml:"autoMountDisks" json:"autoMountDisks"`
}
