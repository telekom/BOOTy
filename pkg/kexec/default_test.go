package kexec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyFixture copies testdata/<name> into a temp root and writes grubenv
// (when env is not nil) next to its grub.cfg.
func copyFixture(t *testing.T, name, grubDir string, env map[string]string) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("testdata", name)
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if env != nil {
		var sb strings.Builder
		sb.WriteString("# GRUB Environment Block\n")
		for k, v := range env {
			sb.WriteString(k + "=" + v + "\n")
		}
		sb.WriteString(strings.Repeat("#", 900))
		if err := os.WriteFile(filepath.Join(root, grubDir, "grubenv"), []byte(sb.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const jammyUUID = "91af7398-5b4d-4006-b4ee-d6d95045b9ef"

func TestDefaultEntryUbuntuCloudImage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		env        map[string]string
		wantTitle  string
		wantArgs   string
		wantKernel string
	}{
		{
			name:      "default 0",
			wantTitle: "Ubuntu", wantKernel: "/boot/vmlinuz-5.15.0-194-generic",
			wantArgs: "root=LABEL=cloudimg-rootfs ro console=tty1 console=ttyS0",
		},
		{
			name:      "next_entry by number path",
			env:       map[string]string{"next_entry": "1>1"},
			wantTitle: "Ubuntu, with Linux 5.15.0-194-generic (recovery mode)", wantKernel: "/boot/vmlinuz-5.15.0-194-generic",
			wantArgs: "root=LABEL=cloudimg-rootfs ro recovery nomodeset dis_ucode_ldr",
		},
		{
			name: "next_entry by id path",
			env: map[string]string{"next_entry": "gnulinux-advanced-" + jammyUUID +
				">gnulinux-5.15.0-194-generic-advanced-" + jammyUUID},
			wantTitle: "Ubuntu, with Linux 5.15.0-194-generic", wantKernel: "/boot/vmlinuz-5.15.0-194-generic",
			wantArgs: "root=LABEL=cloudimg-rootfs ro console=tty1 console=ttyS0",
		},
		{
			// GRUB_DEFAULT=0 ignores saved_entry; only next_entry wins.
			name:      "saved_entry without GRUB_DEFAULT=saved",
			env:       map[string]string{"saved_entry": "1>1"},
			wantTitle: "Ubuntu", wantKernel: "/boot/vmlinuz-5.15.0-194-generic",
			wantArgs: "root=LABEL=cloudimg-rootfs ro console=tty1 console=ttyS0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := copyFixture(t, "ubuntu-2204-cloud", "boot/grub", tc.env)
			e, err := DefaultEntry(root)
			if err != nil {
				t.Fatal(err)
			}
			if e.Name != tc.wantTitle || e.Kernel != tc.wantKernel || e.KernelArgs != tc.wantArgs ||
				e.Initramfs != "/boot/initrd.img-5.15.0-194-generic" {
				t.Fatalf("entry = %+v", e)
			}
		})
	}
}

func TestDefaultEntryRefusesWhatOnlyGRUBCanResolve(t *testing.T) {
	for _, tc := range []struct {
		name, fixture, grubDir string
		env                    map[string]string
		want                   string
	}{
		// Each entry has an initrd and an initrdless linux line in if/else.
		{name: "conditional linux lines", fixture: "ubuntu-2404-gcp", grubDir: "boot/grub", want: "2 linux commands"},
		{name: "BLS", fixture: "centos-stream-9", grubDir: "boot/grub2", want: "BLS"},
		// linux$suf $gptprio_kernel: the kernel comes from gptprio.next.
		{name: "Flatcar", fixture: "flatcar", grubDir: "boot/grub", want: "linux$suf"},
		{name: "unknown default", fixture: "ubuntu-2204-cloud", grubDir: "boot/grub", env: map[string]string{"next_entry": "nope"}, want: `no entry "nope"`},
		{name: "submenu default", fixture: "ubuntu-2204-cloud", grubDir: "boot/grub", env: map[string]string{"next_entry": "1"}, want: "submenu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DefaultEntry(copyFixture(t, tc.fixture, tc.grubDir, tc.env))
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want ErrUnsupported with %q", err, tc.want)
			}
		})
	}
}

func writeGrub(t *testing.T, dir, cfg, env string) string {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, dir)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "grub.cfg"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if env != "" {
		if err := os.WriteFile(filepath.Join(p, "grubenv"), []byte(env), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The bare metal Ubuntu form: GRUB_DEFAULT=saved, $vt_handoff, a variable
// set once at the top, and /boot/grub2.
const savedDefaultCfg = `load_env
if [ "${next_entry}" ] ; then
   set default="${next_entry}"
else
   set default="${saved_entry}"
fi
set console_args="console=ttyS0,115200n8"
function gfxmode {
	set vt_handoff=vt.handoff=7
}
menuentry 'Ubuntu' $menuentry_id_option 'gnulinux-simple' {
	gfxmode $linux_gfx_mode
	linux	/boot/vmlinuz-6.8 root=UUID=abc ro quiet splash $vt_handoff ${console_args}
	initrd	/boot/initrd.img-6.8
}
menuentry 'Ubuntu old' --id gnulinux-old {
	linux	/boot/vmlinuz-6.5 root=UUID=abc ro $unknown
	initrd	/boot/initrd.img-6.5
}
if [ "$grub_platform" = "efi" ]; then
menuentry 'UEFI Firmware Settings' $menuentry_id_option 'uefi-firmware' {
	fwsetup
}
fi
`

func TestDefaultEntrySavedEntryAndVariables(t *testing.T) {
	root := writeGrub(t, "boot/grub2", savedDefaultCfg, "saved_entry=gnulinux-simple\n")
	e, err := DefaultEntry(root)
	if err != nil {
		t.Fatal(err)
	}
	if e.Kernel != "/boot/vmlinuz-6.8" || e.KernelArgs != "root=UUID=abc ro quiet splash console=ttyS0,115200n8" {
		t.Fatalf("entry = %+v, want saved_entry with expanded variables", e)
	}

	_, err = DefaultEntry(writeGrub(t, "boot/grub2", savedDefaultCfg, "saved_entry=gnulinux-old\n"))
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "$unknown") {
		t.Fatalf("err = %v, want ErrUnsupported for $unknown", err)
	}

	// The firmware entry is added only on EFI: its number is unsure.
	_, err = DefaultEntry(writeGrub(t, "boot/grub2", savedDefaultCfg, "saved_entry=2\n"))
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported for a conditional entry number", err)
	}
}

func TestDefaultEntryNoConfig(t *testing.T) {
	if _, err := DefaultEntry(t.TempDir()); err == nil || errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want a missing grub.cfg error", err)
	}
}

func TestShellWords(t *testing.T) {
	got := shellWords(`menuentry 'Ubuntu, with Linux' --class os $menuentry_id_option "id 1" {`)
	want := []string{"menuentry", "Ubuntu, with Linux", "--class", "os", "$menuentry_id_option", "id 1", "{"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("words = %q", got)
	}
}
