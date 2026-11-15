package rescue

import (
	"slices"
	"testing"
)

const (
	testKeyA     = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ8kKf8w6YJ6AW2D8KOa/ms2xcrTgfpgPfemuoTsXRYj a@ops"
	testKeyB     = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIC2j7N8uzGst2VSpIi+UQNVlgq3MVUnL+upBub/+fmwK b@ops"
	testKeyECDSA = "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBNrEkcL10I3vW8qs7S/B6VHQPFSKqx4EiDyal77ugfILV1ZkV8Cue4Ra7BlWUDnlG+avZg/92IadL2JPZ3Q010E= c@ops"
	// An Ed25519 blob that claims to be RSA.
	testKeyMislabelled = "ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAIJ8kKf8w6YJ6AW2D8KOa/ms2xcrTgfpgPfemuoTsXRYj x@ops"
)

func TestParseSSHKeys(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  []string
	}{
		{"empty", "", nil},
		{"only separators", " ; ;\n", nil},
		{"single key", testKeyA, []string{testKeyA}},
		{"single key without comment", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ8kKf8w6YJ6AW2D8KOa/ms2xcrTgfpgPfemuoTsXRYj",
			[]string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ8kKf8w6YJ6AW2D8KOa/ms2xcrTgfpgPfemuoTsXRYj"}},
		{"semicolons", testKeyA + ";" + testKeyB + ";" + testKeyECDSA, []string{testKeyA, testKeyB, testKeyECDSA}},
		{"newlines and blanks", "\n " + testKeyA + " \r\n\n" + testKeyB + "\n", []string{testKeyA, testKeyB}},
		{"options prefix", `from="10.0.0.0/8,192.0.2.0/24",no-pty ` + testKeyA, []string{`from="10.0.0.0/8,192.0.2.0/24",no-pty ` + testKeyA}},
		{"malformed entries dropped", "garbage;ssh-ed25519;ssh-ed25519 not-base64!;" + testKeyMislabelled + ";ssh-ed25519 AAAA;" + testKeyB,
			[]string{testKeyB}},
		{"unknown key type", "ssh-dss AAAAB3NzaC1kc3M= d@ops", nil},
		{"type without key bytes", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5", nil},
		{"trailing bytes", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ8kKf8w6YJ6AW2D8KOa/ms2xcrTgfpgPfemuoTsXRYjAA==", nil},
		{"forced command with a key type", `command="echo ssh-ed25519 only",no-pty ` + testKeyA, []string{`command="echo ssh-ed25519 only",no-pty ` + testKeyA}},
		{"escaped quote in options", `command="echo \" ssh-ed25519 x" ` + testKeyB, []string{`command="echo \" ssh-ed25519 x" ` + testKeyB}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseSSHKeys(tc.value); !slices.Equal(got, tc.want) {
				t.Errorf("ParseSSHKeys(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestDropbearArgsLoadsEachHostKey(t *testing.T) {
	got := dropbearArgs(&Config{LegacySSH: true}, []string{hostKeyPath, rsaHostKeyPath})
	want := []string{"-r", hostKeyPath, "-r", rsaHostKeyPath, "-F", "-E", "-p", "22"}
	if !slices.Equal(got, want) {
		t.Errorf("dropbearArgs() = %q, want %q", got, want)
	}
}

func TestDropbearArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		want []string
	}{
		{"key only", Config{SSHKeys: []string{testKeyA}},
			[]string{"-r", hostKeyPath, "-s", "-j", "-k", "-I", "900", "-F", "-E", "-p", "22"}},
		{"key and password hash", Config{SSHKeys: []string{testKeyA, testKeyB}, PasswordHash: "$6$x$y"},
			[]string{"-r", hostKeyPath, "-s", "-j", "-k", "-I", "900", "-F", "-E", "-p", "22"}},
		{"password hash without key", Config{PasswordHash: "$6$x$y"},
			[]string{"-r", hostKeyPath, "-j", "-k", "-I", "900", "-F", "-E", "-p", "22"}},
		{"nothing configured", Config{},
			[]string{"-r", hostKeyPath, "-s", "-j", "-k", "-I", "900", "-F", "-E", "-p", "22"}},
		{"legacy", Config{SSHKeys: []string{testKeyA}, PasswordHash: "$6$x$y", LegacySSH: true},
			[]string{"-r", hostKeyPath, "-F", "-E", "-p", "22"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := dropbearArgs(&tc.cfg, []string{hostKeyPath}); !slices.Equal(got, tc.want) {
				t.Errorf("dropbearArgs() = %q, want %q", got, tc.want)
			}
		})
	}
}
