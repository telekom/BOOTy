package rescue

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// keyOptions are the authorized_keys options of one RESCUE_SSH_PUBKEY key,
// with the semantics of dropbear (svr-authpubkeyoptions.c): option names
// without letter case, "restrict" is no-port-forwarding plus no-pty (agent
// and X11 forwarding do not exist here), command= keeps its text as written
// (backslashes included) and runs instead of the client's command, which
// goes to SSH_ORIGINAL_COMMAND, permitopen= limits local port forwarding.
type keyOptions struct {
	noPortForwarding bool
	noPTY            bool
	command          string
	hasCommand       bool
	permitOpen       []permitOpen
	noTouchRequired  bool
}

// permitOpen is a permitopen="host:port" destination; port -1 is "*".
type permitOpen struct {
	host string
	port int
}

// authorizedKey is a parsed RESCUE_SSH_PUBKEY entry.
type authorizedKey struct {
	key  ssh.PublicKey
	opts keyOptions
}

// parseAuthorizedKey parses an authorized_keys entry. An unknown or
// malformed option makes the whole key unusable, as in dropbear; so does
// verify-required, which this server cannot check (the Go SSH library does
// not expose the FIDO user verification flag).
func parseAuthorizedKey(entry string) (authorizedKey, error) {
	key, _, options, _, err := ssh.ParseAuthorizedKey([]byte(entry))
	if err != nil {
		return authorizedKey{}, fmt.Errorf("parse key: %w", err)
	}
	ak := authorizedKey{key: key}
	for _, o := range options {
		if err := ak.opts.add(o); err != nil {
			return authorizedKey{}, err
		}
	}
	return ak, nil
}

// keyFlags are the options without a value. no-agent-forwarding and
// no-X11-forwarding change nothing: neither is ever offered.
var keyFlags = map[string]func(*keyOptions){
	"no-port-forwarding":  func(k *keyOptions) { k.noPortForwarding = true },
	"no-pty":              func(k *keyOptions) { k.noPTY = true },
	"restrict":            func(k *keyOptions) { k.noPortForwarding, k.noPTY = true, true },
	"no-agent-forwarding": func(*keyOptions) {},
	"no-x11-forwarding":   func(*keyOptions) {},
	"no-touch-required":   func(k *keyOptions) { k.noTouchRequired = true },
}

func (k *keyOptions) add(o string) error {
	name, value, hasValue := strings.Cut(o, "=")
	name = strings.ToLower(name)
	if set := keyFlags[name]; set != nil && !hasValue {
		set(k)
		return nil
	}
	switch {
	case hasValue && name == "command":
		cmd, ok := quotedOption(value)
		if !ok || k.hasCommand {
			return fmt.Errorf("bad command= option")
		}
		k.command, k.hasCommand = cmd, true
	case hasValue && name == "permitopen":
		p, ok := parsePermitOpen(value)
		if !ok {
			return fmt.Errorf("bad permitopen= option")
		}
		k.permitOpen = append(k.permitOpen, p)
	case !hasValue && name == "verify-required":
		return fmt.Errorf("verify-required is not supported")
	default:
		return fmt.Errorf("unsupported option %q", name)
	}
	return nil
}

// quotedOption returns the text between the quotes of "value", as written.
func quotedOption(v string) (string, bool) {
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return "", false
	}
	return v[1 : len(v)-1], true
}

func parsePermitOpen(v string) (permitOpen, bool) {
	spec, ok := quotedOption(v)
	if !ok {
		return permitOpen{}, false
	}
	host, port, err := net.SplitHostPort(spec)
	if err != nil || host == "" {
		return permitOpen{}, false
	}
	if port == "*" {
		return permitOpen{host: host, port: -1}, true
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return permitOpen{}, false
	}
	return permitOpen{host: host, port: int(n)}, true
}

// allowsOpen reports whether local forwarding to host:port is allowed: no
// permitopen= allows all, else one must match (host as written, no DNS).
func (k *keyOptions) allowsOpen(host string, port int) bool {
	if k.noPortForwarding {
		return false
	}
	if len(k.permitOpen) == 0 {
		return true
	}
	for _, p := range k.permitOpen {
		if p.host == host && (p.port == -1 || p.port == port) {
			return true
		}
	}
	return false
}
