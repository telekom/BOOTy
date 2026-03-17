// Package grubcfg parses grub.cfg files into menu entries.
package grubcfg

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// MenuEntry represents a GRUB menu entry with a title and kernel command line.
type MenuEntry struct {
	Title  string
	Linux  string // linux command-line (kernel + initrd path)
	Initrd string
	Args   string // kernel arguments
}

var menuEntryRe = regexp.MustCompile(`^\s*menuentry\s+['"]([^'"]+)['"]`)

// Parse reads GRUB configuration text and returns parsed menu entries.
func Parse(data string) ([]MenuEntry, error) {
	var entries []MenuEntry
	scanner := bufio.NewScanner(strings.NewReader(data))
	var current *MenuEntry
	depth := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if m := menuEntryRe.FindStringSubmatch(line); m != nil {
			current = &MenuEntry{Title: m[1]}
			if strings.Contains(line, "{") {
				depth++
			}
			continue
		}

		if current == nil {
			continue
		}

		if strings.Contains(line, "{") {
			depth++
		}
		if strings.Contains(line, "}") {
			depth--
			if depth <= 0 {
				entries = append(entries, *current)
				current = nil
				depth = 0
			}
			continue
		}

		switch {
		case strings.HasPrefix(line, "linux"):
			parts := strings.SplitN(line, " ", 3)
			if len(parts) >= 2 {
				current.Linux = parts[1]
			}
			if len(parts) >= 3 {
				current.Args = parts[2]
			}
		case strings.HasPrefix(line, "initrd"):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				current.Initrd = fields[1]
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning grub config: %w", err)
	}

	return entries, nil
}

// ParseFile reads and parses a grub.cfg from disk.
func ParseFile(path string) ([]MenuEntry, error) {
	data, err := os.ReadFile(path) //nolint:gosec // trusted local config path
	if err != nil {
		return nil, fmt.Errorf("reading grub config: %w", err)
	}
	return Parse(string(data))
}

// ExtractMenuEntryTitle extracts the title from a GRUB menuentry line.
func ExtractMenuEntryTitle(line string) string {
	m := menuEntryRe.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}
