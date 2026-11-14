//go:build linux

package disk

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const testLUKSUUID = "0b3c9f4e-6a52-4d2a-9d1e-3c1f0e6b7a10"

// zeroingCommander records the calls like mockCommander and runs dd
// if=/dev/zero of=FILE bs=512 count=N on the test files.
type zeroingCommander struct{ *mockCommander }

func (z zeroingCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := z.mockCommander.Run(ctx, name, args...)
	if err != nil || name != "dd" {
		return out, err
	}
	var path string
	var count int64
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "of="); ok {
			path = v
		}
		if v, ok := strings.CutPrefix(a, "count="); ok {
			count, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_, err = f.WriteAt(make([]byte, count*512), 0)
	return nil, err
}

// luksFixture is a sysfs tree and device files: disk sda with partitions
// sda1 and sda2.
type luksFixture struct {
	sysfs, dev string
	cmd        *mockCommander
	m          *Manager
}

func newLUKSFixture(t *testing.T, sizes map[string]int64) *luksFixture {
	t.Helper()
	f := &luksFixture{sysfs: t.TempDir(), dev: t.TempDir(), cmd: newMockCommander()}
	f.m = newManagerWithSysfs(zeroingCommander{f.cmd}, f.sysfs)
	old := devicePath
	devicePath = func(name string) string { return filepath.Join(f.dev, name) }
	t.Cleanup(func() { devicePath = old })
	for name, size := range sizes {
		dir := filepath.Join(f.sysfs, "block", "sda")
		if name != "sda" {
			dir = filepath.Join(dir, name)
			writeFixtureFile(t, filepath.Join(dir, "partition"), "1")
		}
		writeFixtureFile(t, filepath.Join(dir, "size"), strconv.FormatInt(size/512, 10))
		if err := os.MkdirAll(filepath.Join(f.sysfs, "class", "block"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dir, filepath.Join(f.sysfs, "class", "block", name)); err != nil {
			t.Fatal(err)
		}
		dev, err := os.Create(filepath.Join(f.dev, name))
		if err != nil {
			t.Fatal(err)
		}
		// Not zeros, so a missed byte of the overwrite shows.
		if _, err := dev.Write(bytes.Repeat([]byte{0xa5}, int(size))); err != nil {
			t.Fatal(err)
		}
		if err := dev.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// luks2Header returns one LUKS2 header copy (binary header and JSON area).
func luks2Header(secondary bool, hdrSize, keyslots int64, json string) []byte {
	h := make([]byte, hdrSize)
	if secondary {
		copy(h, luks2SecondaryMagic)
	} else {
		copy(h, luks2Magic)
	}
	binary.BigEndian.PutUint16(h[6:], 2)
	binary.BigEndian.PutUint64(h[8:], uint64(hdrSize))
	copy(h[168:], testLUKSUUID)
	if json == "" {
		json = `{"keyslots":{"0":{"type":"luks2"}},"segments":{"0":{"offset":"` + strconv.FormatInt(2*hdrSize+keyslots, 10) +
			`"}},"config":{"json_size":"` + strconv.FormatInt(hdrSize-4096, 10) + `","keyslots_size":"` + strconv.FormatInt(keyslots, 10) + `"}}`
	}
	copy(h[4096:], json)
	return h
}

// writeLUKS2 puts both header copies on the device file; with primary false
// only the secondary (a primary lost to an earlier signature wipe).
func (f *luksFixture) writeLUKS2(t *testing.T, name string, primary bool, hdrSize, keyslots int64) {
	t.Helper()
	dev, err := os.OpenFile(filepath.Join(f.dev, name), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	if primary {
		if _, err := dev.WriteAt(luks2Header(false, hdrSize, keyslots, ""), 0); err != nil {
			t.Fatal(err)
		}
	} else if _, err := dev.WriteAt(make([]byte, hdrSize), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := dev.WriteAt(luks2Header(true, hdrSize, keyslots, ""), hdrSize); err != nil {
		t.Fatal(err)
	}
}

func TestFindLUKS2FindsPrimaryAndSecondaryHeaders(t *testing.T) {
	for _, primary := range []bool{true, false} {
		f := newLUKSFixture(t, map[string]int64{"sda": 64 << 20, "sda1": 8 << 20, "sda2": 32 << 20})
		f.writeLUKS2(t, "sda2", primary, 16<<10, 1<<20)
		found, unreadable, err := f.m.FindLUKS2([]string{"/dev/sda"})
		if err != nil || len(unreadable) != 0 {
			t.Fatalf("primary=%v: FindLUKS2() = %v, unreadable %v", primary, err, unreadable)
		}
		want := LUKS2Device{Device: filepath.Join(f.dev, "sda2"), UUID: testLUKSUUID, Area: 2*16<<10 + 1<<20}
		if len(found) != 1 || found[0] != want {
			t.Fatalf("primary=%v: found %+v, want %+v", primary, found, want)
		}
	}
}

func TestFindLUKS2AllDisksAndNoLUKS(t *testing.T) {
	f := newLUKSFixture(t, map[string]int64{"sda": 64 << 20, "sda1": 8 << 20})
	found, unreadable, err := f.m.FindLUKS2(nil)
	if err != nil || len(found) != 0 || len(unreadable) != 0 {
		t.Fatalf("FindLUKS2(all) = %+v, %v, %v; want nothing", found, unreadable, err)
	}
	if len(f.cmd.calls) != 0 {
		t.Fatalf("a host without LUKS ran %v", callList(f.cmd.calls))
	}
	f.writeLUKS2(t, "sda1", true, 16<<10, 4<<20)
	if found, _, err = f.m.FindLUKS2(nil); err != nil || len(found) != 1 || found[0].Device != filepath.Join(f.dev, "sda1") {
		t.Fatalf("FindLUKS2(all) = %+v, %v; want sda1", found, err)
	}
}

func TestFindLUKS2SkipsEmptyAndUnreadableDevices(t *testing.T) {
	f := newLUKSFixture(t, map[string]int64{"sda": 64 << 20, "sda1": 0, "sda2": 8 << 20})
	if err := os.Remove(filepath.Join(f.dev, "sda2")); err != nil {
		t.Fatal(err)
	}
	found, unreadable, err := f.m.FindLUKS2([]string{"/dev/sda"})
	if err != nil || len(found) != 0 || len(unreadable) != 1 || unreadable[0] != filepath.Join(f.dev, "sda2") {
		t.Fatalf("FindLUKS2() = %+v, unreadable %v, %v; want sda2 unreadable, sda1 (empty) skipped", found, unreadable, err)
	}
}

func TestFindLUKS2DamagedHeaderFails(t *testing.T) {
	f := newLUKSFixture(t, map[string]int64{"sda": 64 << 20, "sda1": 8 << 20})
	dev, err := os.OpenFile(filepath.Join(f.dev, "sda1"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dev.WriteAt(luks2Header(false, 16<<10, 0, `{"config":`), 0); err != nil {
		t.Fatal(err)
	}
	dev.Close()
	if _, _, err := f.m.FindLUKS2([]string{"/dev/sda"}); !errors.Is(err, errBadLUKS2Header) {
		t.Fatalf("FindLUKS2() = %v, want errBadLUKS2Header", err)
	}
}

// A secondary header magic that is not at its own hdr_size belongs to a
// header that starts elsewhere (a partition seen from its disk): no match.
func TestFindLUKS2IgnoresForeignSecondaryCopy(t *testing.T) {
	f := newLUKSFixture(t, map[string]int64{"sda": 64 << 20})
	dev, err := os.OpenFile(filepath.Join(f.dev, "sda"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dev.WriteAt(luks2Header(true, 1<<20, 0, ""), 2<<20); err != nil {
		t.Fatal(err)
	}
	dev.Close()
	if found, _, err := f.m.FindLUKS2([]string{"/dev/sda"}); err != nil || len(found) != 0 {
		t.Fatalf("FindLUKS2() = %+v, %v; want nothing", found, err)
	}
}

func TestCryptoEraseLUKS2(t *testing.T) {
	f := newLUKSFixture(t, map[string]int64{"sda": 64 << 20, "sda2": 32 << 20})
	f.writeLUKS2(t, "sda2", true, 16<<10, 1<<20)
	found, _, err := f.m.FindLUKS2([]string{"/dev/sda"})
	if err != nil || len(found) != 1 {
		t.Fatalf("FindLUKS2() = %+v, %v", found, err)
	}
	d := found[0]
	if err := f.m.CryptoEraseLUKS2(context.Background(), d); err != nil {
		t.Fatalf("CryptoEraseLUKS2() = %v", err)
	}
	want := []string{
		"cryptsetup luksErase --batch-mode " + d.Device,
		"dd if=/dev/zero of=" + d.Device + " bs=512 count=" + strconv.FormatInt(d.Area/512, 10) + " conv=notrunc,fsync",
	}
	if got := callList(f.cmd.calls); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	data, err := os.ReadFile(d.Device)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data[:d.Area], make([]byte, d.Area)) {
		t.Fatal("the header area is not zeroed")
	}
	if data[d.Area] != 0xa5 {
		t.Fatal("the overwrite went past the header area")
	}
	if found, _, err := f.m.FindLUKS2([]string{"/dev/sda"}); err != nil || len(found) != 0 {
		t.Fatalf("after the erase FindLUKS2() = %+v, %v", found, err)
	}
}

func TestCryptoEraseLUKS2Failures(t *testing.T) {
	for _, tc := range []struct{ fail, want string }{
		{"cryptsetup luksErase", "cryptsetup luksErase"},
		{"dd if=/dev/zero", "overwrite the LUKS2 header area"},
	} {
		f := newLUKSFixture(t, map[string]int64{"sda": 64 << 20, "sda2": 32 << 20})
		f.writeLUKS2(t, "sda2", true, 16<<10, 1<<20)
		found, _, _ := f.m.FindLUKS2([]string{"/dev/sda"})
		f.cmd.setResult(tc.fail, []byte("boom"), errors.New("exit status 1"))
		if err := f.m.CryptoEraseLUKS2(context.Background(), found[0]); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s fails: CryptoEraseLUKS2() = %v, want %q", tc.fail, err, tc.want)
		}
	}
	// A header that is still there after the overwrite (a device that
	// dropped the writes) fails the check.
	f := newLUKSFixture(t, map[string]int64{"sda": 64 << 20, "sda2": 32 << 20})
	f.writeLUKS2(t, "sda2", true, 16<<10, 1<<20)
	found, _, _ := f.m.FindLUKS2([]string{"/dev/sda"})
	f.m.cmd = f.cmd // dd writes nothing
	if err := f.m.CryptoEraseLUKS2(context.Background(), found[0]); err == nil || !strings.Contains(err.Error(), "still on") {
		t.Fatalf("CryptoEraseLUKS2() = %v, want the check to fail", err)
	}
}

// Two paths to one disk (dual-ported SAS without dm-multipath): the second
// finds the header gone and is done.
func TestCryptoEraseLUKS2HeaderAlreadyGone(t *testing.T) {
	f := newLUKSFixture(t, map[string]int64{"sda": 64 << 20, "sda2": 32 << 20})
	f.writeLUKS2(t, "sda2", true, 16<<10, 1<<20)
	found, _, _ := f.m.FindLUKS2([]string{"/dev/sda"})
	if err := os.WriteFile(found[0].Device, make([]byte, 32<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.m.CryptoEraseLUKS2(context.Background(), found[0]); err != nil || len(f.cmd.calls) != 0 {
		t.Fatalf("CryptoEraseLUKS2() = %v, calls %v; want nothing to do", err, callList(f.cmd.calls))
	}
}
