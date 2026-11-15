package rescue

import (
	"encoding/base64"
	"encoding/binary"
	"log/slog"
	"strconv"
	"strings"
)

// sshKeyBlobs maps each accepted public key type to the SSH wire fields
// that follow the type name in its blob: "key32" a 32-byte string, "pN" an
// uncompressed NIST point of N bytes after its curve name, "str" any
// non-empty string. The hardened dropbear build refuses ssh-rsa; it stays
// here for builds with DROPBEAR_HARDENED=false.
var sshKeyBlobs = map[string][]string{
	"ssh-ed25519":                        {"key32"},
	"sk-ssh-ed25519@openssh.com":         {"key32", "str"},
	"ecdsa-sha2-nistp256":                {"nistp256", "p65"},
	"ecdsa-sha2-nistp384":                {"nistp384", "p97"},
	"ecdsa-sha2-nistp521":                {"nistp521", "p133"},
	"sk-ecdsa-sha2-nistp256@openssh.com": {"nistp256", "p65", "str"},
	"ssh-rsa":                            {"str", "str"},
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

// sshKeyType returns the key type of an authorized_keys entry
// "[options] type base64 [comment]" after checking that the blob is a
// complete key of that type. Options are one token in which quoted parts
// may hold spaces, like sshd reads them.
func sshKeyType(entry string) (string, bool) {
	fields := strings.Fields(entry)
	if len(fields) > 0 && sshKeyBlobs[fields[0]] == nil {
		fields = strings.Fields(entry[optionsEnd(entry):])
	}
	if len(fields) < 2 || sshKeyBlobs[fields[0]] == nil {
		return "", false
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", false
	}
	name, blob, ok := sshString(blob)
	if !ok || string(name) != fields[0] {
		return "", false
	}
	for _, want := range sshKeyBlobs[fields[0]] {
		var v []byte
		if v, blob, ok = sshString(blob); !ok || !sshFieldOK(want, v) {
			return "", false
		}
	}
	return fields[0], len(blob) == 0
}

// optionsEnd returns the index of the first space or tab outside double
// quotes (a backslash escapes a quote).
func optionsEnd(entry string) int {
	quoted := false
	for i := 0; i < len(entry); i++ {
		switch c := entry[i]; {
		case c == '\\' && quoted:
			i++
		case c == '"':
			quoted = !quoted
		case (c == ' ' || c == '\t') && !quoted:
			return i
		}
	}
	return len(entry)
}

// sshString reads one SSH wire string (uint32 length, bytes).
func sshString(b []byte) (v, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(n) > uint64(len(b)-4) {
		return nil, nil, false
	}
	return b[4 : 4+n], b[4+n:], true
}

func sshFieldOK(want string, v []byte) bool {
	switch want {
	case "key32":
		return len(v) == 32
	case "str":
		return len(v) > 0
	case "p65", "p97", "p133":
		return want == "p"+strconv.Itoa(len(v)) && v[0] == 4
	default: // curve name
		return string(v) == want
	}
}
