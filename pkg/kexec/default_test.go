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
		{name: "conditional linux lines", fixture: "ubuntu-2404-gcp", grubDir: "boot/grub", want: "linux command inside a condition"},
		{name: "BLS", fixture: "centos-stream-9", grubDir: "boot/grub2", want: "BLS"},
		// linux$suf $gptprio_kernel: the kernel comes from gptprio.next.
		{name: "Flatcar", fixture: "flatcar", grubDir: "boot/grub", want: "configfile"},
		// GRUB would boot next_entry once and clear it; BOOTy can not.
		{name: "next_entry", fixture: "ubuntu-2204-cloud", grubDir: "boot/grub", env: map[string]string{"next_entry": "1>1"}, want: "next_entry"},
		// The Ubuntu initrdless fallback and saved-entry logic.
		{name: "initrdfail", fixture: "ubuntu-2204-cloud", grubDir: "boot/grub", env: map[string]string{"initrdfail": "1"}, want: "initrdfail"},
		{name: "prev_entry", fixture: "ubuntu-2204-cloud", grubDir: "boot/grub", env: map[string]string{"prev_entry": "0"}, want: "prev_entry"},
		{name: "prev_saved_entry", fixture: "ubuntu-2204-cloud", grubDir: "boot/grub", env: map[string]string{"prev_saved_entry": "0"}, want: "prev_saved_entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DefaultEntry(copyFixture(t, tc.fixture, tc.grubDir, tc.env))
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want ErrUnsupported with %q", err, tc.want)
			}
		})
	}
}

// TestDefaultEntryCustomCfg: the 41_custom source line of the Ubuntu
// fixture loads nothing without a custom.cfg, and anything with one.
func TestDefaultEntryCustomCfg(t *testing.T) {
	root := copyFixture(t, "ubuntu-2204-cloud", "boot/grub", nil)
	if _, err := DefaultEntry(root); err != nil {
		t.Fatalf("without custom.cfg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "boot/grub/custom.cfg"), []byte("set default=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DefaultEntry(root); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "custom.cfg") {
		t.Fatalf("with custom.cfg: err = %v, want ErrUnsupported", err)
	}
}

func writeGrub(t *testing.T, cfg, env string) string {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, "boot", "grub2")
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

// The generated GRUB_DEFAULT=saved header of Debian and Ubuntu.
const savedHeader = `load_env
if [ "${next_entry}" ] ; then
   set default="${next_entry}"
   set next_entry=
   save_env next_entry
   set boot_once=true
else
   set default="${saved_entry}"
fi
`

const generatedEntries = `set console_args="console=ttyS0,115200n8"
function gfxmode {
	set vt_handoff=vt.handoff=7
}
menuentry 'Ubuntu' $menuentry_id_option 'gnulinux-simple' {
	recordfail
	load_video
	gfxmode $linux_gfx_mode
	insmod gzio
	if [ x$grub_platform = xxen ]; then insmod xzio; insmod lzopio; fi
	set root='hd0,gpt2'
	if [ x$feature_platform_search_hint = xy ]; then
	  search --no-floppy --fs-uuid --set=root --hint-bios=hd0,gpt2 abc
	else
	  search --no-floppy --fs-uuid --set=root abc
	fi
	echo 'Loading Linux 6.8 ...'
	linux	/boot/vmlinuz-6.8 root=UUID=abc ro quiet splash $vt_handoff ${console_args} 'lit=$console_args' "quoted arg" esc\ aped
	initrd	/boot/initrd.img-6.8
}
submenu 'Advanced options' $menuentry_id_option 'gnulinux-advanced' {
	menuentry 'Ubuntu, with Linux 6.5' --id gnulinux-6.5 {
		linux	/boot/vmlinuz-6.5 root=UUID=abc ro
		initrd	/boot/initrd.img-6.5
	}
}
`

func TestDefaultEntryGeneratedShapes(t *testing.T) {
	cfg := savedHeader + generatedEntries
	for _, tc := range []struct{ env, kernel, args string }{
		{"saved_entry=gnulinux-simple\n", "/boot/vmlinuz-6.8",
			`root=UUID=abc ro quiet splash console=ttyS0,115200n8 lit=$console_args "quoted arg" "esc aped"`},
		{"saved_entry=gnulinux-advanced>gnulinux-6.5\n", "/boot/vmlinuz-6.5", "root=UUID=abc ro"},
		{"saved_entry=Advanced options>Ubuntu, with Linux 6.5\n", "/boot/vmlinuz-6.5", "root=UUID=abc ro"},
		{"saved_entry=1>0\n", "/boot/vmlinuz-6.5", "root=UUID=abc ro"},
		{"", "/boot/vmlinuz-6.8", ""}, // empty saved_entry is entry 0
	} {
		e, err := DefaultEntry(writeGrub(t, cfg, tc.env))
		if err != nil {
			t.Fatalf("%q: %v", tc.env, err)
		}
		if e.Kernel != tc.kernel || tc.args != "" && e.KernelArgs != tc.args {
			t.Fatalf("%q: entry = %+v, want %s %s", tc.env, e, tc.kernel, tc.args)
		}
	}
}

// Every shape outside the allow-list reboots.
func TestDefaultEntryRefusesDoubt(t *testing.T) {
	entry := func(body string) string {
		return "set default=0\nmenuentry 'x' {\n" + body + "\n linux /vmlinuz root=/dev/sda2\n}\n"
	}
	for name, tc := range map[string]struct{ cfg, env, want string }{
		"one-line conditional default":   {cfg: "if [ -s $prefix/grubenv ]; then set default=1; fi\n" + entry(""), want: "conditional default"},
		"multi-line conditional default": {cfg: "if [ x$feature = xy ]; then\n set default=1\nfi\n" + entry(""), want: "conditional default"},
		"elif default":                   {cfg: "if [ \"${next_entry}\" ] ; then\n set default=\"${next_entry}\"\nelif [ x = y ]; then\n set default=1\nfi\n" + entry(""), want: "conditional default"},
		"set in entry":                   {cfg: entry(" set kopts=quiet"), want: "command set"},
		"search other var":               {cfg: entry(" search --set=boot --fs-uuid abc"), want: "command search"},
		"search -s other var":            {cfg: entry(" search -s foo abc"), want: "command search"},
		"probe in entry":                 {cfg: entry(" probe --set=uuid --fs-uuid hd0,gpt2"), want: "command probe"},
		"regexp in entry":                {cfg: entry(" regexp --set=x a b"), want: "command regexp"},
		"source in entry":                {cfg: entry(" source /boot/extra.cfg"), want: "command source"},
		"configfile top level":           {cfg: "configfile /boot/other.cfg\n" + entry(""), want: "configfile"},
		"source top level":               {cfg: "source /boot/x.cfg\n" + entry(""), want: "source"},
		"linux inside if":                {cfg: "set default=0\nmenuentry 'x' {\n if [ x = y ]; then\n linux /a\n fi\n}\n", want: "inside a condition"},
		"one-line linux if":              {cfg: "set default=0\nmenuentry 'x' {\n if [ x = y ]; then linux /a; fi\n}\n", want: "inside a condition"},
		"two linux":                      {cfg: "set default=0\nmenuentry 'x' {\n linux /a\n linux /b\n}\n", want: "2 linux"},
		"two initrd":                     {cfg: entry(" initrd /a\n initrd /b"), want: "2 initrd"},
		"initrd two files":               {cfg: entry(" initrd /ucode.img /initrd.img"), want: "exactly one file"},
		"unknown variable":               {cfg: "set default=0\nmenuentry 'x' {\n linux /a $extra\n}\n", want: "$extra"},
		"variable set twice":             {cfg: "set a=1\nset a=2\nset default=0\nmenuentry 'x' {\n linux /a $a\n}\n", want: "$a"},
		"variable set by search":         {cfg: "search --set=kp abc\nset default=0\nmenuentry 'x' {\n linux /a $kp\n}\n", want: "$kp"},
		"double quote in arg":            {cfg: "set default=0\nmenuentry 'x' {\n linux /a 'x=\"y\"'\n}\n", want: "double quote"},
		"conditional id":                 {cfg: "set default=fw\nif [ x = y ]; then\nmenuentry 'x' --id fw {\n linux /a\n}\nfi\n", want: "no unconditional entry"},
		"conditional title":              {cfg: "set default=x\nif [ x = y ]; then\nmenuentry 'x' {\n linux /a\n}\nfi\n", want: "no unconditional entry"},
		"number after conditional":       {cfg: "set default=1\nif [ x = y ]; then\nmenuentry 'a' {\n linux /a\n}\nfi\nmenuentry 'b' {\n linux /b\n}\n", want: "no unconditional entry"},
		"missing default":                {cfg: "set default=7\nmenuentry 'x' {\n linux /vmlinuz\n}\n", want: "no unconditional entry"},
		"submenu default":                {cfg: "set default=0\nsubmenu 's' {\nmenuentry 'x' {\n linux /a\n}\n}\n", want: "submenu"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DefaultEntry(writeGrub(t, tc.cfg, tc.env))
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want ErrUnsupported with %q", err, tc.want)
			}
		})
	}
}

func TestDefaultEntryNoConfig(t *testing.T) {
	if _, err := DefaultEntry(t.TempDir()); err == nil || errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want a missing grub.cfg error", err)
	}
}

func TestLexWords(t *testing.T) {
	s := &grubScript{vars: map[string]string{"a": "x y"}, unsure: map[string]bool{}}
	for in, want := range map[string]string{
		`linux /k $a`:       "linux|/k|x|y",    // unquoted expansion splits
		`linux /k "$a"`:     "linux|/k|x y",    // double quotes keep one word
		`linux /k '$a'`:     "linux|/k|$a",     // no expansion in single quotes
		`linux /k a\ b \$a`: "linux|/k|a b|$a", // backslash escapes
		`linux /k ${a}z`:    "linux|/k|x|yz",
	} {
		got, err := s.expandWords(in, nil)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if strings.Join(got, "|") != want {
			t.Fatalf("%s: words = %q, want %s", in, got, want)
		}
	}
	if got := strings.Join(literalWords(`menuentry 'A, b' $menuentry_id_option "id 1" {`), "|"); got != "menuentry|A, b|$menuentry_id_option|id 1|{" {
		t.Fatalf("literal words = %q", got)
	}
	if got := splitCommands(`if [ x ]; then insmod a; echo 'x;y'; fi # comment`); len(got) != 4 || got[2] != `echo 'x;y'` {
		t.Fatalf("commands = %q", got)
	}
}
