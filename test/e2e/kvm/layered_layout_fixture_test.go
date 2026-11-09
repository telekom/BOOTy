//go:build e2e

package kvm

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/telekom/BOOTy/pkg/config"
)

// The layered-layout fixture is a small Ubuntu-like whole-disk image (ESP +
// one ext4 root) whose root carries content for the directories that a
// CIS-like layout puts on their own partitions (/var, /var/log,
// /var/log/audit, /var/tmp, /home, /tmp), with the metadata that a
// file-level copy can lose: extended attributes, POSIX ACLs and file
// capabilities. It also ships that layout in BOOTy's PARTITION_LAYOUT format.
// Scenarios that split the root into partitions, sign modules or encrypt data
// volumes build on it.

// layeredLayoutJSONPath is where the fixture image ships its layout
// description, relative to the root.
const layeredLayoutJSONPath = "usr/lib/booty/partition-layout.json"

// layeredLayoutJSON describes the CIS-like layout of the fixture, with the
// nested mountpoints in a different order than their parents.
const layeredLayoutJSON = `{
  "table": "gpt",
  "partitions": [
    {"label": "EFI", "sizeMB": 64, "filesystem": "vfat", "mountpoint": "/boot/efi"},
    {"label": "ROOT", "sizeMB": 768, "filesystem": "ext4", "mountpoint": "/"},
    {"label": "AUDIT", "sizeMB": 32, "filesystem": "ext4", "mountpoint": "/var/log/audit", "mountOptions": "nodev,nosuid,noexec"},
    {"label": "LOG", "sizeMB": 32, "filesystem": "ext4", "mountpoint": "/var/log", "mountOptions": "nodev,nosuid,noexec"},
    {"label": "VARTMP", "sizeMB": 32, "filesystem": "ext4", "mountpoint": "/var/tmp", "mountOptions": "nodev,nosuid,noexec"},
    {"label": "VAR", "sizeMB": 96, "filesystem": "ext4", "mountpoint": "/var", "mountOptions": "nodev,nosuid"},
    {"label": "HOME", "sizeMB": 32, "filesystem": "ext4", "mountpoint": "/home", "mountOptions": "nodev,nosuid"},
    {"label": "TMP", "sizeMB": 0, "filesystem": "ext4", "mountpoint": "/tmp", "mountOptions": "nodev,nosuid,noexec"}
  ]
}
`

// layeredLayoutMounts are the data mountpoints of layeredLayoutJSON with
// their 1-based partition numbers.
var layeredLayoutMounts = []struct {
	part       int
	mountpoint string
}{{3, "/var/log/audit"}, {4, "/var/log"}, {5, "/var/tmp"}, {6, "/var"}, {7, "/home"}, {8, "/tmp"}}

// layeredLayoutDataPartitions are the shared data partitions of the
// system-ab scenario (AB_DATA_PARTITIONS): the layered directories of the
// fixture, in GPT order after EFI, ROOT-A and ROOT-B (partitions 4, 5, 6).
const layeredLayoutDataPartitions = `[` +
	`{"label":"BOOTY-HOME","sizeMB":32,"filesystem":"ext4","mountpoint":"/home"},` +
	`{"label":"BOOTY-TMP","sizeMB":32,"filesystem":"ext4","mountpoint":"/tmp"},` +
	`{"label":"BOOTY-DATA","sizeMB":0,"filesystem":"ext4","mountpoint":"/var"}]`

const (
	xattrCapability = "security.capability"
	xattrACLAccess  = "system.posix_acl_access"
	xattrUserTest   = "user.booty.fixture"
	fixtureUID      = 1000
	fixtureGID      = 1000
	capNetRaw       = 13
	capNetBindSvc   = 10
)

// layeredEntry is one path of the fixture with the metadata it must keep.
type layeredEntry struct {
	path    string // relative to the root
	dir     bool
	content string
	mode    os.FileMode
	uid     int
	gid     int
	xattrs  map[string][]byte
}

// layeredLayoutEntries returns the fixture content. Directories come before
// their children.
func layeredLayoutEntries() []layeredEntry {
	userACL := posixACL(0o6, fixtureUID, 0o4, 0o4, 0o4, 0o0)
	return []layeredEntry{
		{path: "var/lib/layered", dir: true, mode: 0o755,
			xattrs: map[string][]byte{xattrUserTest: []byte("var-dir")}},
		{path: "var/lib/layered/state.txt", content: "var state\n", mode: 0o640, uid: fixtureUID, gid: fixtureGID,
			xattrs: map[string][]byte{xattrUserTest: []byte("var-file"), xattrACLAccess: userACL}},
		{path: "var/lib/layered/ping-helper", content: "#!/bin/sh\nexit 0\n", mode: 0o755,
			xattrs: map[string][]byte{xattrCapability: fileCapability(capNetRaw)}},
		{path: "var/log/layered.log", content: "fixture log line\n", mode: 0o644},
		{path: "var/log/audit", dir: true, mode: 0o750,
			xattrs: map[string][]byte{xattrUserTest: []byte("audit-dir")}},
		{path: "var/log/audit/audit.log", content: "type=DAEMON_START\n", mode: 0o600,
			xattrs: map[string][]byte{xattrUserTest: []byte("audit-file")}},
		{path: "var/tmp", dir: true, mode: 0o777 | os.ModeSticky},
		{path: "var/tmp/cache-helper", content: "#!/bin/sh\nexit 0\n", mode: 0o755,
			xattrs: map[string][]byte{xattrCapability: fileCapability(capNetBindSvc)}},
		{path: "home/tester", dir: true, mode: 0o750, uid: fixtureUID, gid: fixtureGID,
			xattrs: map[string][]byte{xattrUserTest: []byte("home-dir")}},
		{path: "home/tester/.profile", content: "export FIXTURE=1\n", mode: 0o640, uid: fixtureUID, gid: fixtureGID,
			xattrs: map[string][]byte{xattrACLAccess: userACL}},
		{path: "tmp", dir: true, mode: 0o777 | os.ModeSticky},
		{path: "tmp/layered-tmp.txt", content: "tmp content\n", mode: 0o600, uid: fixtureUID, gid: fixtureGID,
			xattrs: map[string][]byte{xattrUserTest: []byte("tmp-file")}},
		{path: "usr/bin/bind-helper", content: "#!/bin/sh\nexit 0\n", mode: 0o755,
			xattrs: map[string][]byte{xattrCapability: fileCapability(capNetBindSvc)}},
		{path: layeredLayoutJSONPath, content: layeredLayoutJSON, mode: 0o644},
	}
}

// posixACL encodes an access ACL (system.posix_acl_access) with one named
// user entry, in the byte form the kernel returns from getxattr.
func posixACL(userObj uint16, user uint32, userPerm, groupObj, mask, other uint16) []byte {
	const undefinedID = 0xffffffff
	type entry struct {
		tag  uint16
		perm uint16
		id   uint32
	}
	entries := []entry{
		{0x01, userObj, undefinedID},  // ACL_USER_OBJ
		{0x02, userPerm, user},        // ACL_USER
		{0x04, groupObj, undefinedID}, // ACL_GROUP_OBJ
		{0x10, mask, undefinedID},     // ACL_MASK
		{0x20, other, undefinedID},    // ACL_OTHER
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint32(2)) // POSIX_ACL_XATTR_VERSION
	for _, e := range entries {
		_ = binary.Write(&buf, binary.LittleEndian, e)
	}
	return buf.Bytes()
}

// fileCapability encodes security.capability (VFS_CAP_REVISION_2, effective)
// with one permitted capability.
func fileCapability(capability uint) []byte {
	data := []uint32{0x02000000 | 0x1, 0, 0, 0, 0} // magic_etc, permitted/inheritable x2
	data[1+2*(capability/32)] = 1 << (capability % 32)
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, data)
	return buf.Bytes()
}

// createLayeredLayoutTestDiskImage builds the fixture: the chroot-capable
// test image (ESP + ext4 root) plus layeredLayoutEntries in the root.
func createLayeredLayoutTestDiskImage(t *testing.T, sizeMB int) string {
	t.Helper()
	rawPath := createChrootCapableTestDiskImage(t, sizeMB)
	withLoopRoot(t, rawPath, false, func(root string) {
		for _, e := range layeredLayoutEntries() {
			writeLayeredEntry(t, root, &e)
		}
	})
	return rawPath
}

// withLoopRoot mounts partition 2 of a raw image and calls fn with the
// mount path.
func withLoopRoot(t *testing.T, rawPath string, readOnly bool, fn func(root string)) {
	t.Helper()
	loopDev := strings.TrimSpace(string(runOutput(t, "attach fixture loop device", "losetup", "--find", "--show", "--partscan", rawPath)))
	defer func() { _ = exec.Command("losetup", "-d", loopDev).Run() }()
	rootDev := loopDev + "p2"
	waitForDevice(t, rootDev, 5*time.Second)
	mountDir := filepath.Join(t.TempDir(), "layered-root")
	if err := os.MkdirAll(mountDir, 0o755); err != nil {
		t.Fatalf("mkdir fixture mountpoint: %v", err)
	}
	if readOnly {
		mountReadOnlyWithRetry(t, rootDev, mountDir, nil)
	} else {
		mountWithRetry(t, "mount fixture root", rootDev, mountDir)
	}
	defer func() { _ = exec.Command("umount", mountDir).Run() }()
	fn(mountDir)
	run(t, "unmount fixture root", "umount", mountDir)
}

func writeLayeredEntry(t *testing.T, root string, e *layeredEntry) {
	t.Helper()
	p := filepath.Join(root, e.path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", e.path, err)
	}
	if e.dir {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", e.path, err)
		}
	} else if err := os.WriteFile(p, []byte(e.content), 0o600); err != nil {
		t.Fatalf("write %s: %v", e.path, err)
	}
	// Owner before mode (chown clears setuid bits) and both before the
	// xattrs: chown drops security.capability, chmod rewrites the ACL mask.
	if err := os.Lchown(p, e.uid, e.gid); err != nil {
		t.Fatalf("chown %s: %v", e.path, err)
	}
	if err := os.Chmod(p, e.mode); err != nil {
		t.Fatalf("chmod %s: %v", e.path, err)
	}
	for name, value := range e.xattrs {
		if err := unix.Lsetxattr(p, name, value, 0); err != nil {
			t.Fatalf("setxattr %s on %s: %v", name, e.path, err)
		}
	}
}

// layeredCheck selects what assertLayeredEntries compares.
type layeredCheck struct {
	// skipXattrs leaves xattrs, ACLs and file capabilities out.
	skipXattrs bool
	// exclude leaves out the entries below these prefixes (for example
	// "var/log/"), which are on a nested mount.
	exclude []string
}

func (c layeredCheck) excludes(path string) bool {
	for _, prefix := range c.exclude {
		if strings.HasPrefix(path+"/", prefix) {
			return true
		}
	}
	return false
}

// assertLayeredEntries checks the fixture entries below prefix (for
// example "var/") in root, which is the mount of the partition that holds
// prefix; strip removes prefix from the path for a data partition mount.
func assertLayeredEntries(t *testing.T, root, prefix string, strip bool, check layeredCheck) {
	t.Helper()
	found := 0
	for _, e := range layeredLayoutEntries() {
		if !strings.HasPrefix(e.path+"/", prefix) || check.excludes(e.path) {
			continue
		}
		found++
		rel := e.path
		if strip {
			rel = strings.TrimPrefix(strings.TrimPrefix(e.path, strings.TrimSuffix(prefix, "/")), "/")
		}
		assertLayeredEntry(t, filepath.Join(root, rel), &e, check)
	}
	if found == 0 {
		t.Fatalf("no fixture entries below %q", prefix)
	}
}

func assertLayeredEntry(t *testing.T, p string, e *layeredEntry, check layeredCheck) {
	t.Helper()
	info, err := os.Lstat(p)
	if err != nil {
		t.Errorf("%s: %v", e.path, err)
		return
	}
	if info.IsDir() != e.dir {
		t.Errorf("%s: dir = %v, want %v", e.path, info.IsDir(), e.dir)
	}
	if got := info.Mode() & (os.ModePerm | os.ModeSticky | os.ModeSetuid | os.ModeSetgid); got != e.mode {
		t.Errorf("%s: mode = %v, want %v", e.path, got, e.mode)
	}
	if uid, gid := ownerOf(info); uid != e.uid || gid != e.gid {
		t.Errorf("%s: owner = %d:%d, want %d:%d", e.path, uid, gid, e.uid, e.gid)
	}
	if !e.dir {
		data, err := os.ReadFile(p)
		if err != nil || string(data) != e.content {
			t.Errorf("%s: content = %q (%v), want %q", e.path, data, err, e.content)
		}
	}
	if check.skipXattrs {
		return
	}
	for name, want := range e.xattrs {
		got, err := getXattr(p, name)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: xattr %s = %x (%v), want %x", e.path, name, got, err, want)
		}
	}
}

func getXattr(path, name string) ([]byte, error) {
	buf := make([]byte, 256)
	n, err := unix.Lgetxattr(path, name, buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func ownerOf(info os.FileInfo) (uid, gid int) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, -1
	}
	return int(st.Uid), int(st.Gid)
}

func assertLayeredLayoutJSON(t *testing.T, root string) {
	t.Helper()
	data := readProvisionedFile(t, root, layeredLayoutJSONPath)
	layout, err := config.ParsePartitionLayout(data)
	if err != nil {
		t.Fatalf("fixture layout JSON: %v", err)
	}
	var mountpoints []string
	for _, p := range layout.Partitions {
		mountpoints = append(mountpoints, p.Mountpoint)
	}
	if got := strings.Join(mountpoints, ","); got != "/boot/efi,/,/var/log/audit,/var/log,/var/tmp,/var,/home,/tmp" {
		t.Fatalf("fixture layout mountpoints = %s", got)
	}
}
