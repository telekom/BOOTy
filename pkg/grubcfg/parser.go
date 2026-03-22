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
			depth += countBraces(line)
			continue
		}

		if current == nil {
			continue
		}

		depth, current, entries = processLine(line, depth, current, entries)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning grub config: %w", err)
	}

	return entries, nil
}

func countBraces(line string) int {
	n := 0
	if strings.Contains(line, "{") {
		n++
	}
	if strings.Contains(line, "}") {
		n--
	}
	return n
}

func processLine(line string, depth int, current *MenuEntry, entries []MenuEntry) (newDepth int, newCurrent *MenuEntry, newEntries []MenuEntry) {
	// Skip comment lines to avoid braces in comments breaking depth tracking.
	if strings.HasPrefix(line, "#") {
		return depth, current, entries
	}
	if strings.Contains(line, "{") {
		depth++
	}
	if strings.Contains(line, "}") {
		depth--
		if depth <= 0 {
			entries = append(entries, *current)
			return 0, nil, entries
		}
		return depth, current, entries
	}

	parseLinuxOrInitrd(line, current)
	return depth, current, entries
}

func parseLinuxOrInitrd(line string, entry *MenuEntry) {
	switch {
	case strings.HasPrefix(line, "linux"):
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			entry.Linux = fields[1]
		}
		if len(fields) >= 3 {
			entry.Args = strings.Join(fields[2:], " ")
		}
	case strings.HasPrefix(line, "initrd"):
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			entry.Initrd = fields[1]
		}
	}
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
