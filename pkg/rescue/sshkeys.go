package rescue

import (
	"encoding/base64"
	"encoding/binary"
	"log/slog"
	"strings"
)

// sshKeyTypes are the public key types an authorized_keys entry may carry.
// The hardened dropbear build refuses ssh-rsa; it stays here for builds
// with DROPBEAR_HARDENED=false.
var sshKeyTypes = map[string]bool{
	"ssh-ed25519":                        true,
	"sk-ssh-ed25519@openssh.com":         true,
	"ecdsa-sha2-nistp256":                true,
	"ecdsa-sha2-nistp384":                true,
	"ecdsa-sha2-nistp521":                true,
	"sk-ecdsa-sha2-nistp256@openssh.com": true,
	"ssh-rsa":                            true,
}

// ParseSSHKeys splits a RESCUE_SSH_PUBKEY value into authorized_keys
// entries. Entries are separated by ";" or newlines; empty entries are
// skipped. An entry is "[options] type base64 [comment]" like an
// authorized_keys line. Malformed entries are logged and dropped, so one bad
// key does not lock out the others.
func ParseSSHKeys(value string) []string {
	var keys []string
	for i, entry := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' }) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		keyType, ok := sshKeyType(entry)
		if !ok {
			slog.Warn("ignoring malformed rescue SSH key", "entry", i+1)
			continue
		}
		if keyType == "ssh-rsa" {
			slog.Warn("rescue SSH key is RSA, which the hardened dropbear build refuses", "entry", i+1)
		}
		keys = append(keys, entry)
	}
	return keys
}

// sshKeyType returns the key type of an authorized_keys entry, after
// checking that the base64 blob that follows the type token encodes a key
// of that type.
func sshKeyType(entry string) (string, bool) {
	fields := strings.Fields(entry)
	for i := 0; i+1 < len(fields); i++ {
		if !sshKeyTypes[fields[i]] {
			continue
		}
		blob, err := base64.StdEncoding.DecodeString(fields[i+1])
		if err != nil || len(blob) < 4 {
			return "", false
		}
		n := binary.BigEndian.Uint32(blob)
		if uint64(n) > uint64(len(blob)-4) || string(blob[4:4+n]) != fields[i] {
			return "", false
		}
		return fields[i], true
	}
	return "", false
}
