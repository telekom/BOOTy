//go:build linux

package provision

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/disk"
)

const cisLikeLayoutJSON = `{"partitions":[` +
	`{"label":"EFI","sizeMB":64,"filesystem":"vfat","mountpoint":"/boot/efi"},` +
	`{"label":"ROOT","sizeMB":768,"filesystem":"ext4","mountpoint":"/"},` +
	`{"label":"LOG","sizeMB":64,"filesystem":"ext4","mountpoint":"/var/log","mountOptions":"nodev,nosuid,noexec"},` +
	`{"label":"VAR","sizeMB":128,"filesystem":"ext4","mountpoint":"/var","mountOptions":"nodev,nosuid"},` +
	`{"label":"pv"}],` +
	`"lvm":{"volumeGroup":"sysvg","pvPartition":5,"volumes":[` +
	`{"name":"audit","sizeMB":32,"filesystem":"ext4","mountpoint":"/var/log/audit","mountOptions":"nodev,nosuid,noexec"},` +
	`{"name":"swap","sizeMB":32,"filesystem":"swap"},` +
	`{"name":"tmp","filesystem":"xfs","mountpoint":"/tmp","mountOptions":"nodev,nosuid,noexec"}]}}`

func mustParseLayout(t *testing.T, data string) *config.PartitionLayout {
	t.Helper()
	layout, err := config.ParsePartitionLayout(data)
	if err != nil {
		t.Fatalf("ParsePartitionLayout: %v", err)
	}
	return layout
}

func TestSharedDataMountsFromLayoutOrdersNestedMounts(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.PartitionLayout = mustParseLayout(t, cisLikeLayoutJSON)
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	o.targetDisk = "/dev/sda"

	mounts := o.sharedDataMountsFromLayout()
	var got []string
	for _, m := range mounts {
		got = append(got, m.device+"="+m.target)
	}
	want := []string{
		"/dev/sda4=/newroot/var",
		"/dev/sysvg/tmp=/newroot/tmp",
		"/dev/sda3=/newroot/var/log",
		"/dev/sysvg/audit=/newroot/var/log/audit",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("mounts = %v, want %v", got, want)
	}
	tests := []struct {
		mount int
		want  string
	}{
		{0, "/newroot/var/log,/newroot/var/log/audit"},
		{1, ""},
		{2, "/newroot/var/log/audit"},
		{3, ""},
	}
	for _, tt := range tests {
		if got := strings.Join(nestedMountTargets(mounts[tt.mount], mounts), ","); got != tt.want {
			t.Errorf("nestedMountTargets(%s) = %q, want %q", mounts[tt.mount].mountpoint, got, tt.want)
		}
	}
}

func TestMountSharedDataMountsNonABLayoutParentsFirst(t *testing.T) {
	cfg := &config.MachineConfig{}
	cfg.Provision.Disk.PartitionLayout = mustParseLayout(t, cisLikeLayoutJSON)
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	o.targetDisk = "/dev/sda"

	oldMountPoint := isMountPoint
	isMountPoint = func(string) bool { return false }
	t.Cleanup(func() { isMountPoint = oldMountPoint })
	oldMountShared := mountSharedDataPart
	var mounted []string
	mountSharedDataPart = func(_ context.Context, _ *disk.Manager, device, mountpoint string) error {
		mounted = append(mounted, device+"="+mountpoint)
		return nil
	}
	t.Cleanup(func() { mountSharedDataPart = oldMountShared })

	if err := o.mountSharedData(context.Background()); err != nil {
		t.Fatalf("mountSharedData: %v", err)
	}
	want := "/dev/sda4=/newroot/var,/dev/sysvg/tmp=/newroot/tmp,/dev/sda3=/newroot/var/log,/dev/sysvg/audit=/newroot/var/log/audit"
	if got := strings.Join(mounted, ","); got != want {
		t.Fatalf("mounted = %s, want %s", got, want)
	}
	if got := strings.Join(o.sharedMounts, ","); got != "/newroot/var,/newroot/tmp,/newroot/var/log,/newroot/var/log/audit" {
		t.Fatalf("sharedMounts = %s", got)
	}
}

func TestMountSharedDataSkipsDualRootAndPlainWholeDisk(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  func(*config.MachineConfig)
	}{
		{"whole-disk without layout", func(*config.MachineConfig) {}},
		{"dual-root", func(c *config.MachineConfig) {
			c.Provision.Image.Mode = config.ImageModeAB
			layout, err := c.Provision.AB.PartitionLayout("/dev/sda")
			if err != nil {
				t.Fatal(err)
			}
			c.Provision.Disk.PartitionLayout = layout
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			tc.cfg(cfg)
			o := newTestOrchestrator(t, cfg, &mockProvider{})
			o.targetDisk = "/dev/sda"
			oldMountShared := mountSharedDataPart
			mountSharedDataPart = func(context.Context, *disk.Manager, string, string) error {
				t.Fatal("unexpected data mount")
				return nil
			}
			t.Cleanup(func() { mountSharedDataPart = oldMountShared })
			if err := o.mountSharedData(context.Background()); err != nil {
				t.Fatalf("mountSharedData: %v", err)
			}
		})
	}
}

func TestValidateSupportedLayoutMountpoint(t *testing.T) {
	for _, tc := range []struct {
		mountpoint string
		wantErr    bool
	}{
		{"", false},
		{"/", false},
		{"/boot/efi", false},
		{"/var", false},
		{"/var/log/audit", false},
		{"/home", false},
		{"/boot", true},
		{"/boot/", true},
		{"/var/", true},
		{"//var", true},
		{"/boot/efi/EFI", true},
		{"/proc", true},
		{"/run/user", true},
		{"/dev", true},
		{"/sys/fs", true},
		{"/srv", false},
		{"/etc", true},
		{"/etc/ssl", false},
		{"/etc/booty", true},
		{"/etc/booty/keys", true},
		{"/etc/bootyx", false},
		{"/boot/grub", true},
		{"/boot/firmware", true},
		{`/srv/cache\040files`, true},
		{"/srv/cache files", true},
		{"/srv/cache\tfiles", true},
		{"/srv/cache\nfiles", true},
		{"/srv/cache\u00a0files", true},
		{"/srv/cache\x7ffiles", true},
		{"/srv/cache\x01files", true},
		{"/srv/cache-files", false},
	} {
		err := validateSupportedLayoutMountpoint(tc.mountpoint, "ext4", map[string]bool{})
		if (err != nil) != tc.wantErr {
			t.Errorf("validateSupportedLayoutMountpoint(%q) = %v, wantErr %v", tc.mountpoint, err, tc.wantErr)
		}
	}
}

// TestCopyTreeWithSymlinksKeepsXattrsAndSkipsNested checks the seed copy:
// user xattrs, POSIX ACLs and file capabilities survive, and a nested
// mountpoint is created with its metadata but without its content.
func TestCopyTreeWithSymlinksKeepsXattrsAndSkipsNested(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	mustMkdir(t, filepath.Join(src, "lib"), 0o750)
	mustMkdir(t, filepath.Join(src, "log"), 0o755)
	writeFile(t, filepath.Join(src, "lib", "state"), "state")
	writeFile(t, filepath.Join(src, "lib", "helper"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(src, "log", "nested.log"), "nested")

	acl := testPosixACL(1000)
	capability := []byte{0x01, 0x00, 0x00, 0x02, 0x00, 0x20, 0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	xattrs := []struct {
		path, name string
		value      []byte
	}{
		{"lib", "user.booty.test", []byte("dir")},
		{"lib/state", "user.booty.test", []byte("file")},
		{"lib/state", "system.posix_acl_access", acl},
		{"log", "user.booty.test", []byte("mountpoint")},
	}
	// Setting a file capability needs CAP_SETFCAP, and a trusted.* xattr
	// (allowed on a symlink, unlike user.*) CAP_SYS_ADMIN; the kvm scenario
	// covers capabilities for unprivileged runs.
	if os.Geteuid() == 0 {
		xattrs = append(xattrs, struct {
			path, name string
			value      []byte
		}{"lib/helper", "security.capability", capability})
		link := filepath.Join(src, "lib", "link")
		if err := os.Symlink("state", link); err != nil {
			t.Fatal(err)
		}
		if unix.Lsetxattr(link, "trusted.booty.test", []byte("symlink"), 0) == nil {
			xattrs = append(xattrs, struct {
				path, name string
				value      []byte
			}{"lib/link", "trusted.booty.test", []byte("symlink")})
		}
	}
	for _, x := range xattrs {
		if err := unix.Lsetxattr(filepath.Join(src, x.path), x.name, x.value, 0); err != nil {
			if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EPERM) {
				t.Skipf("set %s on the test filesystem: %v", x.name, err)
			}
			t.Fatalf("set %s on %s: %v", x.name, x.path, err)
		}
	}

	if err := copyTreeWithSymlinks(context.Background(), src, dst, filepath.Join(src, "log")); err != nil {
		t.Fatalf("copyTreeWithSymlinks: %v", err)
	}
	for _, x := range xattrs {
		got, err := getXattr(filepath.Join(dst, x.path), x.name)
		if err != nil || !bytes.Equal(got, x.value) {
			t.Errorf("%s %s = %x (%v), want %x", x.path, x.name, got, err, x.value)
		}
	}
	if info, err := os.Stat(filepath.Join(dst, "log")); err != nil || !info.IsDir() {
		t.Fatalf("nested mountpoint directory missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "log", "nested.log")); !os.IsNotExist(err) {
		t.Fatalf("nested mountpoint content copied: %v", err)
	}
}

// An ACL inherited from a default ACL that an interrupted seed left on the
// parent is removed when the source has none.
func TestCopyXattrsDropsACLMissingInSource(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	writeFile(t, src, "src")
	writeFile(t, dst, "dst")
	setTestACL(t, dst, "system.posix_acl_access", testPosixACL(1000))
	if err := copyXattrs(src, dst); err != nil {
		t.Fatalf("copyXattrs: %v", err)
	}
	wantNoACL(t, dst, "system.posix_acl_access")
}

func TestCopyXattrsDropsDefaultACLMissingInSource(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	mustMkdir(t, src, 0o755)
	mustMkdir(t, dst, 0o755)
	setTestACL(t, dst, "system.posix_acl_default", testPosixACL(1000))
	if err := copyXattrs(src, dst); err != nil {
		t.Fatalf("copyXattrs: %v", err)
	}
	wantNoACL(t, dst, "system.posix_acl_default")
}

// The ACLs of the source replace those of the destination.
func TestCopyXattrsCopiesSourceACL(t *testing.T) {
	dir := t.TempDir()
	srcDir, dstDir := filepath.Join(dir, "srcdir"), filepath.Join(dir, "dstdir")
	srcFile, dstFile := filepath.Join(dir, "srcfile"), filepath.Join(dir, "dstfile")
	mustMkdir(t, srcDir, 0o755)
	mustMkdir(t, dstDir, 0o755)
	writeFile(t, srcFile, "src")
	writeFile(t, dstFile, "dst")
	setTestACL(t, srcDir, "system.posix_acl_access", testPosixACL(1000))
	setTestACL(t, srcDir, "system.posix_acl_default", testPosixACL(1001))
	setTestACL(t, dstDir, "system.posix_acl_default", testPosixACL(1002))
	setTestACL(t, srcFile, "system.posix_acl_access", testPosixACL(1000))
	for _, c := range []struct{ src, dst string }{{srcDir, dstDir}, {srcFile, dstFile}} {
		if err := copyXattrs(c.src, c.dst); err != nil {
			t.Fatalf("copyXattrs %s: %v", c.src, err)
		}
	}
	for _, x := range []struct {
		path, name string
		value      []byte
	}{
		{dstDir, "system.posix_acl_access", testPosixACL(1000)},
		{dstDir, "system.posix_acl_default", testPosixACL(1001)},
		{dstFile, "system.posix_acl_access", testPosixACL(1000)},
	} {
		if got, err := getXattr(x.path, x.name); err != nil || !bytes.Equal(got, x.value) {
			t.Errorf("%s %s = %x (%v), want %x", x.path, x.name, got, err, x.value)
		}
	}
}

// A directory at the destination of a source symlink is kept with its own
// ACLs: the xattrs of the symlink do not apply to it.
func TestCopyTreeWithSymlinksKeepsDirectoryACLForSourceSymlink(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.Symlink("elsewhere", filepath.Join(src, "m")); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(dst, "m"), 0o755)
	setTestACL(t, filepath.Join(dst, "m"), "system.posix_acl_default", testPosixACL(1000))
	if err := copyTreeWithSymlinks(context.Background(), src, dst); err != nil {
		t.Fatalf("copyTreeWithSymlinks: %v", err)
	}
	if got, err := getXattr(filepath.Join(dst, "m"), "system.posix_acl_default"); err != nil || !bytes.Equal(got, testPosixACL(1000)) {
		t.Fatalf("default ACL of the kept directory = %x (%v)", got, err)
	}
}

// A retried seed after an interrupted one: the destination root still has
// the default ACL of the source root that the first run copied, but the
// source no longer has it. Nothing created on the retry keeps the
// inherited ACLs.
func TestCopyTreeWithSymlinksDropsStaleDefaultACLOnRetry(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	mustMkdir(t, filepath.Join(src, "lib"), 0o755)
	writeFile(t, filepath.Join(src, "lib", "state"), "state")
	writeFile(t, filepath.Join(src, "top"), "top")
	setTestACL(t, dst, "system.posix_acl_default", testPosixACL(1000))
	if err := copyTreeWithSymlinks(context.Background(), src, dst); err != nil {
		t.Fatalf("copyTreeWithSymlinks: %v", err)
	}
	wantNoACL(t, dst, "system.posix_acl_default")
	for _, rel := range []string{"lib", "lib/state", "top"} {
		wantNoACL(t, filepath.Join(dst, rel), "system.posix_acl_access")
	}
	wantNoACL(t, filepath.Join(dst, "lib"), "system.posix_acl_default")
}

func setTestACL(t *testing.T, path, name string, value []byte) {
	t.Helper()
	if err := unix.Lsetxattr(path, name, value, 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EPERM) {
			t.Skipf("set %s on the test filesystem: %v", name, err)
		}
		t.Fatalf("set %s on %s: %v", name, path, err)
	}
}

func wantNoACL(t *testing.T, path, name string) {
	t.Helper()
	if got, err := getXattr(path, name); !errors.Is(err, unix.ENODATA) {
		t.Errorf("%s %s = %x (%v), want none", path, name, got, err)
	}
}

func testPosixACL(uid uint32) []byte {
	le16 := func(v uint16) []byte { return []byte{byte(v), byte(v >> 8)} }
	le32 := func(v uint32) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }
	buf := le32(2)
	for _, e := range []struct {
		tag, perm uint16
		id        uint32
	}{{0x01, 6, 0xffffffff}, {0x02, 4, uid}, {0x04, 4, 0xffffffff}, {0x10, 4, 0xffffffff}, {0x20, 0, 0xffffffff}} {
		buf = append(buf, le16(e.tag)...)
		buf = append(buf, le16(e.perm)...)
		buf = append(buf, le32(e.id)...)
	}
	return buf
}

func mustMkdir(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestFetchPartitionLayout(t *testing.T) {
	inline := mustParseLayout(t, `{"partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`)
	for _, tc := range []struct {
		name       string
		body       string
		status     int
		inline     *config.PartitionLayout
		wantErr    string
		wantLabels string
	}{
		{name: "fetches layout", body: cisLikeLayoutJSON, status: http.StatusOK, wantLabels: "EFI,ROOT,LOG,VAR,pv"},
		{name: "PARTITION_LAYOUT overrides", body: cisLikeLayoutJSON, status: http.StatusOK, inline: inline, wantLabels: "root"},
		{name: "rejects device", body: `{"device":"/dev/sda","partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`, status: http.StatusOK, wantErr: "must not set device"},
		{name: "rejects invalid layout", body: `{"partitions":[]}`, status: http.StatusOK, wantErr: "no partitions"},
		{name: "rejects 404", status: http.StatusNotFound, wantErr: "404"},
		{name: "rejects large document", body: strings.Repeat(" ", maxPartitionLayoutSize+1), status: http.StatusOK, wantErr: "larger than"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			cfg := &config.MachineConfig{}
			cfg.Provision.Disk.PartitionLayoutURL = srv.URL + "/layout.json"
			cfg.Provision.Disk.PartitionLayout = tc.inline
			o := newTestOrchestrator(t, cfg, &mockProvider{})

			err := o.fetchPartitionLayout(context.Background())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("fetchPartitionLayout: %v", err)
			}
			var labels []string
			for _, p := range cfg.Provision.Disk.PartitionLayout.Partitions {
				labels = append(labels, p.Label)
			}
			if got := strings.Join(labels, ","); got != tc.wantLabels {
				t.Fatalf("labels = %s, want %s", got, tc.wantLabels)
			}
			if (o.sourcePartitionLayout != nil) != (tc.inline == nil) {
				t.Fatalf("sourcePartitionLayout set = %v", o.sourcePartitionLayout != nil)
			}
		})
	}
}

func TestReadPartitionLayoutFile(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "usr/share/vendor/layout"), 0o755)
	writeFile(t, filepath.Join(root, "usr/share/vendor/layout/cis.json"), cisLikeLayoutJSON)
	mustMkdir(t, filepath.Join(root, "usr/share/two"), 0o755)
	writeFile(t, filepath.Join(root, "usr/share/two/a.json"), "{}")
	writeFile(t, filepath.Join(root, "usr/share/two/b.json"), "{}")
	outside := filepath.Join(t.TempDir(), "outside.json")
	writeFile(t, outside, cisLikeLayoutJSON)
	mustMkdir(t, filepath.Join(root, "usr/share/link"), 0o755)
	if err := os.Symlink(outside, filepath.Join(root, "usr/share/link/layout.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "usr/share/dirlink")); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		pattern string
		wantErr string
	}{
		{"/usr/share/vendor/layout/*.json", ""},
		{"/usr/share/vendor/layout/cis.json", ""},
		{"/usr/share/missing/*.json", "matches 0 files"},
		{"/usr/share/two/*.json", "matches 2 files"},
		{"/usr/share/link/layout.json", "symlink"},
		{"/usr/share/dirlink/outside.json", "symlink"},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			data, err := readPartitionLayoutFile(root, tc.pattern)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || string(data) != cisLikeLayoutJSON {
				t.Fatalf("data = %q, err = %v", data, err)
			}
		})
	}
}

func TestLoadImagePartitionLayoutSkipsWithoutPathOrWithOverride(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		layout *config.PartitionLayout
	}{
		{name: "no path"},
		{name: "PARTITION_LAYOUT overrides", path: "/usr/share/vendor/layout/*.json",
			layout: mustParseLayout(t, `{"partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			cfg.Provision.Disk.PartitionLayoutImagePath = tc.path
			cfg.Provision.Disk.PartitionLayout = tc.layout
			o := newTestOrchestrator(t, cfg, &mockProvider{})
			oldMount := mountReadOnlyPart
			mountReadOnlyPart = func(context.Context, *disk.Manager, string, string) error {
				t.Fatal("unexpected image root mount")
				return nil
			}
			t.Cleanup(func() { mountReadOnlyPart = oldMount })
			if err := o.loadImagePartitionLayout(context.Background()); err != nil {
				t.Fatalf("loadImagePartitionLayout: %v", err)
			}
		})
	}
}

func TestCheckpointKeepsSourcePartitionLayout(t *testing.T) {
	layout := mustParseLayout(t, cisLikeLayoutJSON)
	cfg := &config.MachineConfig{}
	o := newTestOrchestrator(t, cfg, &mockProvider{})
	o.sourcePartitionLayout = layout
	cp := &Checkpoint{}
	o.recordCheckpointDerivedState("load-image-partition-layout", cp)
	if cp.PartitionLayout != layout {
		t.Fatal("checkpoint did not record the source layout")
	}

	resumed := newTestOrchestrator(t, &config.MachineConfig{}, &mockProvider{})
	resumed.restoreCheckpointDerivedState(cp)
	if resumed.cfg.Provision.Disk.PartitionLayout != layout || resumed.sourcePartitionLayout != layout {
		t.Fatal("resume did not restore the source layout")
	}

	inline := mustParseLayout(t, `{"partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`)
	withVars := &config.MachineConfig{}
	withVars.Provision.Disk.PartitionLayout = inline
	keeps := newTestOrchestrator(t, withVars, &mockProvider{})
	keeps.restoreCheckpointDerivedState(cp)
	if keeps.cfg.Provision.Disk.PartitionLayout != inline {
		t.Fatal("checkpoint layout replaced PARTITION_LAYOUT")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadImagePartitionLayoutRejectsBadImageLayouts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{name: "missing file", wantErr: "matches 0 files"},
		{name: "device in layout", files: map[string]string{
			"usr/share/vendor/layout/cis.json": `{"device":"/dev/sda","partitions":[{"label":"root","filesystem":"ext4","mountpoint":"/"}]}`,
		}, wantErr: "must not set device"},
		{name: "invalid layout", files: map[string]string{
			"usr/share/vendor/layout/cis.json": `{"partitions":[{"label":"data","filesystem":"ext4","mountpoint":"/data"}]}`,
		}, wantErr: "mountpoint \"/\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.MachineConfig{}
			cfg.Provision.Disk.PartitionLayoutImagePath = "/usr/share/vendor/layout/*.json"
			o := newTestOrchestrator(t, cfg, &mockProvider{})
			o.rootPartition = "/dev/vda2"
			oldMount := mountReadOnlyPart
			mountReadOnlyPart = func(_ context.Context, _ *disk.Manager, device, mountpoint string) error {
				if device != "/dev/vda2" {
					t.Errorf("mounted %s, want the image root /dev/vda2", device)
				}
				t.Cleanup(func() { _ = os.RemoveAll(mountpoint) })
				for rel, content := range tc.files {
					mustMkdir(t, filepath.Join(mountpoint, filepath.Dir(rel)), 0o755)
					writeFile(t, filepath.Join(mountpoint, rel), content)
				}
				return nil
			}
			t.Cleanup(func() { mountReadOnlyPart = oldMount })
			oldUnmount := unmountSharedDataPart
			unmountSharedDataPart = func(*disk.Manager, string) error { return nil }
			t.Cleanup(func() { unmountSharedDataPart = oldUnmount })

			err := o.loadImagePartitionLayout(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if !isPermanent(err) {
				t.Fatalf("err = %v, want a permanent error", err)
			}
			if cfg.Provision.Disk.PartitionLayout != nil {
				t.Fatal("a rejected image layout was kept")
			}
		})
	}
}

func TestValidateSupportedLayoutMountpointVFATOnlyForESP(t *testing.T) {
	if err := validateSupportedLayoutMountpoint("/boot/efi", "vfat", map[string]bool{}); err != nil {
		t.Fatalf("vfat /boot/efi: %v", err)
	}
	if err := validateSupportedLayoutMountpoint("/boot/firmware", "VFAT", map[string]bool{}); err == nil {
		t.Fatal("vfat data mountpoint /boot/firmware accepted")
	}
}

func TestValidateSupportedLayoutMountpointRejectsDuplicates(t *testing.T) {
	seen := map[string]bool{}
	if err := validateSupportedLayoutMountpoint("/var", "ext4", seen); err != nil {
		t.Fatal(err)
	}
	if err := validateSupportedLayoutMountpoint("/var", "ext4", seen); err == nil {
		t.Fatal("duplicate /var accepted")
	}
}

func TestResumeRepeatsWholeDiskWriteBeforeImageLayout(t *testing.T) {
	steps := []string{"disable-lvm", "wipe-disks", "apply-partition-layout", "stream-image", "partprobe", "parse-partitions"}
	for _, tc := range []struct {
		name      string
		loaded    bool
		layout    bool
		inMemory  bool
		wantSteps string
	}{
		{name: "interrupted load forgets the write", wantSteps: "parse-partitions"},
		{name: "completed load keeps the steps", loaded: true, layout: true, wantSteps: strings.Join(steps, ",")},
		// RESCUE_MODE=retry runs Provision again on the same orchestrator:
		// the layout of the failed run must not stand in for the checkpoint.
		{name: "retry after a failed load forgets the write", inMemory: true, wantSteps: "parse-partitions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp := &Checkpoint{CompletedSteps: append([]string(nil), steps...)}
			if tc.loaded {
				cp.MarkStep("load-image-partition-layout")
			}
			if tc.layout {
				cp.PartitionLayout = mustParseLayout(t, cisLikeLayoutJSON)
			}
			cfg := &config.MachineConfig{}
			cfg.Provision.Disk.PartitionLayoutImagePath = "/usr/share/vendor/layout/*.json"
			o := newTestOrchestrator(t, cfg, &mockProvider{})
			if tc.inMemory {
				if err := o.useSourcePartitionLayout([]byte(cisLikeLayoutJSON), "PARTITION_LAYOUT_IMAGE_PATH"); err != nil {
					t.Fatal(err)
				}
			}
			o.restoreCheckpointDerivedState(cp)
			if tc.inMemory && (cfg.Provision.Disk.PartitionLayout != nil || o.sourcePartitionLayout != nil) {
				t.Fatal("the layout of the failed run was kept")
			}
			got := strings.Join(slices.DeleteFunc(cp.CompletedSteps, func(s string) bool { return s == "load-image-partition-layout" }), ",")
			if got != tc.wantSteps {
				t.Fatalf("completed steps = %s, want %s", got, tc.wantSteps)
			}
		})
	}
}

// An image root mount that load-image-partition-layout could not unmount is
// released by release-stale-mounts before a retry writes the disk again.
func TestReleaseStaleMountsUnmountsImageLayoutMount(t *testing.T) {
	o := newTestOrchestrator(t, &config.MachineConfig{}, &mockProvider{})
	mnt := t.TempDir()
	unmountErr := errors.New("device busy")
	var unmounted []string
	oldMountPoint, oldUnmount, oldTree := isMountPoint, unmountSharedDataPart, unmountTargetTree
	t.Cleanup(func() { isMountPoint, unmountSharedDataPart, unmountTargetTree = oldMountPoint, oldUnmount, oldTree })
	isMountPoint = func(path string) bool { return path == mnt }
	unmountSharedDataPart = func(_ *disk.Manager, path string) error {
		unmounted = append(unmounted, path)
		return unmountErr
	}
	unmountTargetTree = func(*disk.Manager, string) error { return nil }

	// The read fails to unmount: the mount is kept for the retry.
	oldMount := mountReadOnlyPart
	t.Cleanup(func() { mountReadOnlyPart = oldMount })
	mountReadOnlyPart = func(_ context.Context, _ *disk.Manager, _, mountpoint string) error {
		mnt = mountpoint
		return nil
	}
	o.rootPartition = "/dev/vda2"
	if _, err := o.readImagePartitionLayout(context.Background(), "/layout.json"); !errors.Is(err, unmountErr) {
		t.Fatalf("readImagePartitionLayout err = %v, want the unmount error", err)
	}
	if o.imageLayoutMount != mnt {
		t.Fatalf("imageLayoutMount = %q, want %q", o.imageLayoutMount, mnt)
	}
	if err := o.releaseStaleTargetMounts(context.Background()); !errors.Is(err, unmountErr) {
		t.Fatalf("releaseStaleTargetMounts err = %v, want the unmount error", err)
	}
	unmountErr = nil
	if err := o.releaseStaleTargetMounts(context.Background()); err != nil {
		t.Fatalf("releaseStaleTargetMounts: %v", err)
	}
	if o.imageLayoutMount != "" || len(unmounted) != 3 {
		t.Fatalf("imageLayoutMount = %q, unmounts %v", o.imageLayoutMount, unmounted)
	}
}
