package grubcfg

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Entry represents one GRUB menuentry block.
type Entry struct {
	Title   string
	Kernel  string
	Initrd  string
	Cmdline string
}

// ParseFile parses a grub.cfg file from disk.
func ParseFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open grub config: %w", err)
	}
	defer func() { _ = f.Close() }()

	return Parse(f)
}

// Parse parses GRUB menu entries from a reader.
func Parse(r io.Reader) ([]Entry, error) {
	var entries []Entry
	var current *Entry
	braceDepth := 0

	scanner := bufio.NewScanner(r)
	// Real-world kernel command lines can exceed Scanner's default 64KiB token limit.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "menuentry ") {
			current = &Entry{Title: ExtractMenuEntryTitle(line)}
			braceDepth = braceDelta(line)
			if braceDepth <= 0 {
				braceDepth = 1
			}
			continue
		}

		if current == nil {
			continue
		}

		parseEntryLine(line, current)
		braceDepth += braceDelta(line)
		if braceDepth > 0 {
			continue
		}

		entries = append(entries, *current)
		current = nil
		braceDepth = 0
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan grub config: %w", err)
	}

	if current != nil {
		entries = append(entries, *current)
	}

	return entries, nil
}

// ExtractMenuEntryTitle extracts the entry title from a menuentry line.
func ExtractMenuEntryTitle(line string) string {
	for _, q := range []byte{'\'', '"'} {
		start := strings.IndexByte(line, q)
		if start < 0 {
			continue
		}
		end := strings.IndexByte(line[start+1:], q)
		if end < 0 {
			continue
		}
		return line[start+1 : start+1+end]
	}

	name := strings.TrimPrefix(strings.TrimSpace(line), "menuentry")
	name = strings.TrimSpace(name)
	if braceIdx := strings.IndexByte(name, '{'); braceIdx >= 0 {
		name = strings.TrimSpace(name[:braceIdx])
	}
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func parseEntryLine(line string, current *Entry) {
	switch {
	case strings.HasPrefix(line, "linux ") || strings.HasPrefix(line, "linuxefi ") || strings.HasPrefix(line, "linux16 "):
		parseKernelLine(line, current)
	case strings.HasPrefix(line, "initrd ") || strings.HasPrefix(line, "initrdefi ") || strings.HasPrefix(line, "initrd16 "):
		parseInitrdLine(line, current)
	}
}

func parseKernelLine(line string, current *Entry) {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		current.Kernel = parts[1]
	}
	if len(parts) >= 3 {
		current.Cmdline = strings.Join(parts[2:], " ")
	}
}

func parseInitrdLine(line string, current *Entry) {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		current.Initrd = parts[1]
	}
}

func braceDelta(line string) int {
	return strings.Count(line, "{") - strings.Count(line, "}")
}
