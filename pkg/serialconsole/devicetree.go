package serialconsole

import (
	"fmt"
	"path"
	"strings"
)

const maxStdoutPathBytes = 512

// StdoutPath is a parsed device-tree /chosen/stdout-path value.
type StdoutPath struct {
	// Node is either an alias name (for example "serial0") or a full
	// device-tree node path (for example "/soc/serial@7e215040").
	Node string
	// Spec holds the console options declared after the colon, if any.
	Spec Spec
	Raw  string
}

// IsAlias reports whether the node reference is an alias rather than a path.
func (s *StdoutPath) IsAlias() bool { return !strings.HasPrefix(s.Node, "/") }

// ParseStdoutPath parses a device-tree stdout-path property such as
// "serial0:115200n8" or "/soc/serial@7e215040:115200n8".
func ParseStdoutPath(data []byte) (StdoutPath, error) {
	if len(data) > maxStdoutPathBytes {
		return StdoutPath{}, fmt.Errorf("device-tree stdout-path is %d bytes, maximum is %d",
			len(data), maxStdoutPathBytes)
	}
	raw := strings.TrimSpace(strings.TrimRight(string(data), "\x00\n"))
	if raw == "" {
		return StdoutPath{}, fmt.Errorf("device-tree stdout-path is empty")
	}
	node, options, hasOptions := strings.Cut(raw, ":")
	node = strings.TrimSpace(node)
	if node == "" {
		return StdoutPath{}, fmt.Errorf("device-tree stdout-path %q names no node", raw)
	}
	result := StdoutPath{Node: node, Raw: raw}
	if hasOptions && strings.TrimSpace(options) != "" {
		spec, err := ParseSpec("dtcons0," + strings.TrimSpace(options))
		if err != nil {
			return StdoutPath{}, fmt.Errorf("device-tree stdout-path %q has invalid options: %w", raw, err)
		}
		spec.Device = ""
		result.Spec = spec
	}
	return result, nil
}

// resolveStdoutNode expands an alias into a full device-tree node path using
// the read-only aliases node.
func (r *Resolver) resolveStdoutNode(stdout *StdoutPath) (string, error) {
	if !stdout.IsAlias() {
		return path.Clean(stdout.Node), nil
	}
	if err := validateAliasName(stdout.Node); err != nil {
		return "", err
	}
	value := r.readAttr(path.Join(r.deviceTreeRoot(), "aliases", stdout.Node))
	value = strings.TrimSpace(strings.TrimRight(value, "\x00"))
	if value == "" {
		return "", fmt.Errorf("device-tree alias %q does not resolve to a node", stdout.Node)
	}
	if !strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("device-tree alias %q resolves to non-absolute node %q", stdout.Node, value)
	}
	return path.Clean(value), nil
}

func validateAliasName(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("device-tree alias name %q is outside the supported length", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("device-tree alias name %q contains unsupported characters", name)
		}
	}
	return nil
}

// portForOfNode finds the tty backed by the given device-tree node path.
func portForOfNode(ports []port, node string) (port, bool) {
	node = path.Clean(node)
	for _, p := range ports {
		if p.OfNode != "" && path.Clean(p.OfNode) == node {
			return p, true
		}
	}
	return port{}, false
}
