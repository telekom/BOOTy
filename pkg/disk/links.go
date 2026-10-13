package disk

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// diskLinkDirs maps blkid export keys to the /dev/disk directories that
// udev's 60-persistent-storage.rules create for them.
var diskLinkDirs = []struct{ key, dir string }{
	{"UUID", "by-uuid"},
	{"PARTUUID", "by-partuuid"},
	{"LABEL", "by-label"},
	{"PARTLABEL", "by-partlabel"},
}

// CreateDiskLinks creates the /dev/disk/by-uuid, by-partuuid, by-label and
// by-partlabel links below devDir from blkid, like udev does on the
// installed system. The ramdisk has no udev. Without the links grub-mkconfig
// (update-grub) does not find /dev/disk/by-uuid/<root UUID> and writes
// root=/dev/sdXN, which breaks the boot when the disk order changes.
// Existing links in these directories are replaced. When two devices carry
// the same value, the first one in blkid order wins.
func (m *Manager) CreateDiskLinks(ctx context.Context, devDir string) error {
	// -c /dev/null: probe the devices, never trust a blkid cache file.
	out, err := m.cmd.Run(ctx, "blkid", "-c", "/dev/null", "-o", "export")
	if err != nil && strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("blkid: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// blkid exits 2 without output when no device has a signature.

	for _, d := range diskLinkDirs {
		dir := filepath.Join(devDir, "disk", d.dir)
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	created := 0
	for _, dev := range parseBlkidExport(string(out)) {
		name, ok := strings.CutPrefix(dev["DEVNAME"], "/dev/")
		if !ok || name == "" {
			continue
		}
		for _, d := range diskLinkDirs {
			value := dev[d.key]
			if value == "" {
				continue
			}
			link := filepath.Join(devDir, "disk", d.dir, encodeDevLinkName(value))
			if err := os.Symlink("../../"+name, link); err != nil {
				if os.IsExist(err) {
					slog.Warn("duplicate disk link, keeping the first device", "link", link, "device", dev["DEVNAME"])
					continue
				}
				return fmt.Errorf("create %s: %w", link, err)
			}
			created++
		}
	}
	slog.Info("created /dev/disk links", "count", created)
	return nil
}

// parseBlkidExport parses `blkid -o export`: KEY=value lines, one block per
// device, blocks separated by empty lines.
func parseBlkidExport(out string) []map[string]string {
	var devices []map[string]string
	cur := map[string]string{}
	flush := func() {
		if len(cur) > 0 {
			devices = append(devices, cur)
			cur = map[string]string{}
		}
	}
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			cur[key] = unescapeBlkidValue(value)
		}
	}
	flush()
	return devices
}

// unescapeBlkidValue undoes the backslash escaping of `blkid -o export`.
func unescapeBlkidValue(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) {
			i++
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

// encodeDevLinkName escapes a link name like udev's encode_devnode_name:
// ASCII bytes outside [A-Za-z0-9#+-.:=@_] become \xHH, so a label with a slash
// or a space stays one path element.
func encodeDevLinkName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80 || strings.IndexByte("#+-.:=@_", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, `\x%02x`, c)
	}
	return b.String()
}
