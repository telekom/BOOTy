//go:build linux

package disk

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// LUKS2 on-disk format (cryptsetup docs/on-disk-format-luks2.pdf): a binary
// header of 4096 bytes followed by the JSON area, twice (the second copy
// starts at hdr_size), then the keyslots area of config.keyslots_size.
const (
	luks2MinHeader       = 16 << 10
	luks2MaxHeader       = 4 << 20
	luks2MaxKeyslotsArea = 128 << 20
	luks2BinaryHeader    = 4096
)

var (
	luks2Magic          = []byte("LUKS\xba\xbe")
	luks2SecondaryMagic = []byte("SKUL\xba\xbe")
	// devicePath is the device node of a kernel block device name. A
	// variable for tests.
	devicePath = func(name string) string { return "/dev/" + name }
)

// LUKS2Device is a LUKS2 header that CryptoEraseLUKS2 destroys.
type LUKS2Device struct {
	Device string
	UUID   string
	// Area is the number of bytes from the start of the device that hold
	// both header copies and the keyslots area.
	Area int64
}

// FindLUKS2 returns the LUKS2 headers on disks and on their partitions:
// the disks that deprovisioning wipes. No disks means every disk that
// WipeAllDisks wipes; like the wipe it skips disks in use. A named disk in
// use is an error, as in WipeDisk. A device that cannot be read or whose
// partitions cannot be listed is skipped and returned in unreadable; a header that is found but cannot be parsed is
// an error. It reads the headers itself, so a host without LUKS needs no
// cryptsetup.
func (m *Manager) FindLUKS2(disks []string) (found []LUKS2Device, unreadable []string, err error) {
	if len(disks) == 0 {
		if disks, err = m.disksNotInUse(); err != nil {
			return nil, nil, err
		}
	} else {
		for _, d := range disks {
			if err := m.CheckNotInUse(d); err != nil {
				return nil, nil, err
			}
		}
	}
	for _, d := range disks {
		names, err := m.deviceAndPartitions(blockName(d))
		if err != nil {
			slog.Warn("cannot list the partitions to look for LUKS2 headers", "device", d, "error", err)
			unreadable = append(unreadable, d)
			continue
		}
		for _, name := range names {
			dev, err := m.readLUKS2Device(name)
			var unreadableErr *unreadableDeviceError
			switch {
			case errors.Is(err, errNoLUKS2):
			case errors.As(err, &unreadableErr):
				slog.Warn("cannot read the device to look for a LUKS2 header", "device", unreadableErr.device, "error", unreadableErr.err)
				unreadable = append(unreadable, unreadableErr.device)
			case err != nil:
				return nil, nil, err
			default:
				found = append(found, dev)
			}
		}
	}
	return found, unreadable, nil
}

// disksNotInUse lists the disks of WipeAllDisks without those in use.
func (m *Manager) disksNotInUse() ([]string, error) {
	blockDir := filepath.Join(m.sysfsRoot, "block")
	entries, err := os.ReadDir(blockDir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", blockDir, err)
	}
	var disks []string
	for _, entry := range entries {
		if isVirtualDisk(entry.Name()) {
			continue
		}
		dev := "/dev/" + entry.Name()
		err := m.CheckNotInUse(dev)
		var inUse *InUseError
		if errors.As(err, &inUse) {
			continue // the wipe skips it and reports it
		}
		if err != nil {
			return nil, err
		}
		disks = append(disks, dev)
	}
	return disks, nil
}

// deviceAndPartitions returns name and the names of its partitions from
// sysfs.
func (m *Manager) deviceAndPartitions(name string) ([]string, error) {
	dir := filepath.Join(m.sysfsRoot, "class", "block", name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list partitions of %s: %w", name, err)
	}
	names := []string{name}
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "partition")); err == nil {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

type unreadableDeviceError struct {
	device string
	err    error
}

func (e *unreadableDeviceError) Error() string { return e.device + ": " + e.err.Error() }

var (
	errNoLUKS2        = errors.New("no LUKS2 header")
	errBadLUKS2Header = errors.New("LUKS2 header magic found, but the header does not parse")
)

// readLUKS2Device returns the LUKS2 header of the block device name, or
// errNoLUKS2. An empty device (a card reader without media) has none.
func (m *Manager) readLUKS2Device(name string) (LUKS2Device, error) {
	path := devicePath(name)
	sectors, err := os.ReadFile(filepath.Join(m.sysfsRoot, "class", "block", name, "size"))
	if err != nil {
		return LUKS2Device{}, &unreadableDeviceError{path, err}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(sectors)), 10, 64)
	if err != nil {
		return LUKS2Device{}, &unreadableDeviceError{path, err}
	}
	if n == 0 {
		return LUKS2Device{}, errNoLUKS2
	}
	dev, err := readLUKS2(path)
	dev.Area = min(dev.Area, n*512)
	return dev, err
}

// readLUKS2 returns the first LUKS2 header copy of path that parses: the
// primary at 0 or a secondary at 16 KiB to 4 MiB. A copy whose magic is
// found but that does not parse is an error when no other copy parses.
func readLUKS2(path string) (LUKS2Device, error) {
	f, err := os.Open(path) //nolint:gosec // a block device of the wipe scope
	if err != nil {
		return LUKS2Device{}, &unreadableDeviceError{path, err}
	}
	defer f.Close() //nolint:errcheck // read only
	// Drop cached blocks: another path to the same disk (dual-ported SAS
	// without dm-multipath) may have changed them. Fails on a regular file.
	_ = unix.IoctlSetInt(int(f.Fd()), unix.BLKFLSBUF, 0) //nolint:gosec // fd fits in int
	result := errNoLUKS2
	for off := int64(0); off <= luks2MaxHeader; off = max(2*off, luks2MinHeader) {
		dev, err := parseLUKS2Header(f, off)
		switch {
		case err == nil:
			dev.Device = path
			return dev, nil
		case errors.Is(err, errBadLUKS2Header):
			result = fmt.Errorf("%s: %w", path, err)
		case !errors.Is(err, errNoLUKS2):
			return LUKS2Device{}, &unreadableDeviceError{path, err}
		}
	}
	return LUKS2Device{}, result
}

// parseLUKS2Header reads the header copy at off, or returns errNoLUKS2.
func parseLUKS2Header(r io.ReaderAt, off int64) (LUKS2Device, error) {
	bin := make([]byte, luks2BinaryHeader)
	if _, err := r.ReadAt(bin, off); errors.Is(err, io.EOF) {
		return LUKS2Device{}, errNoLUKS2 // the device ends before this copy
	} else if err != nil {
		return LUKS2Device{}, fmt.Errorf("read offset %d: %w", off, err)
	}
	magic := luks2Magic
	if off > 0 {
		magic = luks2SecondaryMagic
	}
	// hdr_offset (byte 256) is where the copy says it is. A secondary
	// copy that is not at its own hdr_offset belongs to a header that
	// starts elsewhere, such as a partition seen from its disk.
	hdrOffset := binary.BigEndian.Uint64(bin[256:264])
	if !bytes.Equal(bin[:6], magic) || binary.BigEndian.Uint16(bin[6:8]) != 2 || (off > 0 && hdrOffset != uint64(off)) {
		return LUKS2Device{}, errNoLUKS2
	}
	hdrSize := int64(binary.BigEndian.Uint64(bin[8:16])) //nolint:gosec // checked below
	if hdrSize < luks2MinHeader || hdrSize > luks2MaxHeader || hdrSize&(hdrSize-1) != 0 || (off > 0 && off != hdrSize) {
		return LUKS2Device{}, fmt.Errorf("%w: hdr_size %d at offset %d", errBadLUKS2Header, hdrSize, off)
	}
	js := make([]byte, hdrSize-luks2BinaryHeader)
	if _, err := r.ReadAt(js, off+luks2BinaryHeader); err != nil {
		return LUKS2Device{}, fmt.Errorf("%w: JSON area at offset %d: %w", errBadLUKS2Header, off, err)
	}
	keyslots, err := luks2KeyslotsSize(js)
	if err != nil {
		return LUKS2Device{}, fmt.Errorf("%w: offset %d: %w", errBadLUKS2Header, off, err)
	}
	// uuid: char[40] at byte 168 of the binary header.
	return LUKS2Device{UUID: string(bytes.TrimRight(bin[168:208], "\x00")), Area: 2*hdrSize + keyslots}, nil
}

// luks2KeyslotsSize returns config.keyslots_size of the JSON area.
func luks2KeyslotsSize(js []byte) (int64, error) {
	if i := bytes.IndexByte(js, 0); i >= 0 {
		js = js[:i]
	}
	var meta struct {
		Config struct {
			KeyslotsSize string `json:"keyslots_size"`
		} `json:"config"`
	}
	if err := json.Unmarshal(js, &meta); err != nil {
		return 0, fmt.Errorf("JSON: %w", err)
	}
	n, err := strconv.ParseInt(meta.Config.KeyslotsSize, 10, 64)
	if err != nil || n < 0 || n > luks2MaxKeyslotsArea || n%luks2BinaryHeader != 0 {
		return 0, fmt.Errorf("keyslots_size %q", meta.Config.KeyslotsSize)
	}
	return n, nil
}

// CryptoEraseLUKS2 destroys every keyslot of d with cryptsetup luksErase,
// then writes zeros over both header copies and the keyslots area, and
// checks that no header copy is left. The data area stays: without a key
// it is noise.
func (m *Manager) CryptoEraseLUKS2(ctx context.Context, d LUKS2Device) error {
	if _, err := readLUKS2(d.Device); errors.Is(err, errNoLUKS2) {
		// Erased through another path to the same disk.
		slog.Info("LUKS2 header already gone", "device", d.Device, "luksUUID", d.UUID)
		return nil
	}
	if out, err := m.cmd.Run(ctx, "cryptsetup", "luksErase", "--batch-mode", d.Device); err != nil {
		return fmt.Errorf("cryptsetup luksErase %s: %s: %w", d.Device, strings.TrimSpace(string(out)), err)
	}
	// Through the commander like the wipe, so a test double writes nothing.
	args := []string{"if=/dev/zero", "of=" + d.Device, "bs=512", "count=" + strconv.FormatInt(d.Area/512, 10), "conv=notrunc,fsync"}
	if out, err := m.cmd.Run(ctx, "dd", args...); err != nil {
		return fmt.Errorf("overwrite the LUKS2 header area of %s: %s: %w", d.Device, strings.TrimSpace(string(out)), err)
	}
	switch _, err := readLUKS2(d.Device); {
	case errors.Is(err, errNoLUKS2):
		return nil
	case err != nil:
		return fmt.Errorf("check the crypto-erase of %s: %w", d.Device, err)
	default:
		return fmt.Errorf("a LUKS2 header is still on %s after the crypto-erase", d.Device)
	}
}
