package caprf

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveEntry struct {
	name, body, link string
	typ              byte
	mode             int64
}

func deployArchive(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Typeflag: tar.TypeReg, Linkname: e.link, Uid: 4242, Gid: 4242}
		if e.typ != 0 {
			hdr.Typeflag = e.typ
		}
		if e.mode != 0 {
			hdr.Mode = e.mode
		}
		if hdr.Typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const baseAgentVars = "TOKEN=agent-token\nMODE=standby\nINIT_URL=https://caprf/status/init\n" +
	"HEARTBEAT_URL=https://caprf/agent/heartbeat\nCOMMANDS_URL=https://caprf/agent/commands\n" +
	"FIRMWARE_URL=https://caprf/firmware\nHOSTNAME=old\n"

// validVars is a deploy/vars that passes, so in the unsafe-entry tests only
// the guard under test can fail the archive.
var validVars = archiveEntry{name: "deploy/vars", body: "HOSTNAME=worker-7\n"}

// useDeployRoot points deployRoot at a temp /deploy with files of a
// previous job and returns it.
func useDeployRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "deploy")
	prev := deployRoot
	t.Cleanup(func() { deployRoot = prev })
	deployRoot = root
	for _, f := range []string{"machine-files/old", "machine-commands/old.sh", "file-system/etc/old", "vars"} {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The archive of the next job replaces the per-job files; the agent keeps
// its token, endpoints and bearer URLs.
func TestDeployArchiveConfigReplacesJobFiles(t *testing.T) {
	root := useDeployRoot(t)
	payload := deployArchive(t,
		archiveEntry{name: "deploy/", typ: tar.TypeDir},
		archiveEntry{name: "deploy/vars", body: "TOKEN=job-token\nMODE=provision\nFIRMWARE_URL=https://evil/fw\nHOSTNAME=worker-7\n"},
		archiveEntry{name: "deploy/machine-files/etc/cloud/nocloud/user-data", body: "#cloud-config\n", mode: 0o4666},
		archiveEntry{name: "deploy/machine-commands/10-run.sh", body: "#!/bin/sh\n", mode: 0o700},
	)

	c := &Client{vars: []byte(baseAgentVars)}
	cfg, keys, err := c.CommandConfig(payload)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hostname != "worker-7" || cfg.Transport.Token != "agent-token" || cfg.Mode != "standby" ||
		cfg.Provision.Firmware.URL != "https://caprf/firmware" {
		t.Fatalf("hostname %q token %q mode %q firmware %q, want the archive hostname and the live agent settings",
			cfg.Hostname, cfg.Transport.Token, cfg.Mode, cfg.Provision.Firmware.URL)
	}
	if strings.Join(keys, ",") != "FIRMWARE_URL,HOSTNAME,MODE,TOKEN" {
		t.Fatalf("keys = %v", keys)
	}
	for file, mode := range map[string]os.FileMode{
		"machine-files/etc/cloud/nocloud/user-data": 0o644, // setuid and world write dropped
		"machine-commands/10-run.sh":                0o755,
	} {
		fi, err := os.Lstat(filepath.Join(root, file))
		if err != nil || fi.Mode() != mode {
			t.Fatalf("%s: mode %v, %v; want %v", file, fi.Mode(), err, mode)
		}
	}
	for _, gone := range []string{"machine-files/old", "file-system"} {
		if _, err := os.Lstat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s of the previous job still exists (%v)", gone, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "vars")); string(b) != "old" {
		t.Fatal("the archive changed /deploy/vars")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(root), ".booty-archive-*")); len(left) != 0 {
		t.Fatalf("staging left behind: %v", left)
	}
}

// Each archive has a valid deploy/vars: only the guard can reject it, and
// /deploy stays unchanged.
func TestDeployArchiveRejectsUnsafeEntries(t *testing.T) {
	defer func(b int64, n int) { maxDeployArchive, maxArchiveEntries = b, n }(maxDeployArchive, maxArchiveEntries)
	maxDeployArchive, maxArchiveEntries = 64, 5
	for name, entries := range map[string][]archiveEntry{
		// Proven tar-slip: a symlink, then a file of the same name.
		"symlink then file": {validVars, {name: "deploy/machine-files/x", typ: tar.TypeSymlink, link: "/etc/passwd"}, {name: "deploy/machine-files/x", body: "pwned"}},
		"symlink":           {validVars, {name: "deploy/machine-files/l", typ: tar.TypeSymlink, link: "../"}},
		"top-level symlink": {validVars, {name: "deploy/machine-files", typ: tar.TypeSymlink, link: "/etc"}},
		"vars symlink":      {{name: "deploy/vars", typ: tar.TypeSymlink, link: "/etc/shadow"}},
		"hard link":         {validVars, {name: "deploy/machine-files/h", typ: tar.TypeLink, link: "deploy/vars"}},
		"device":            {validVars, {name: "deploy/machine-files/null", typ: tar.TypeChar}},
		"duplicate file":    {validVars, {name: "deploy/machine-files/a", body: "1"}, {name: "deploy/machine-files/a", body: "2"}},
		"outside deploy":    {validVars, {name: "etc/passwd", body: "x"}},
		"dot dot":           {validVars, {name: "deploy/../etc/passwd", body: "x"}},
		"dot dot inside":    {validVars, {name: "deploy/machine-files/../../etc/passwd", body: "x"}},
		"absolute":          {validVars, {name: "/deploy/machine-files/a", body: "x"}},
		"empty name":        {validVars, {name: "", body: "x"}},
		"too large":         {validVars, {name: "deploy/machine-files/big", body: strings.Repeat("x", 65)}},
		"too many entries":  {validVars, {name: "deploy/a/1", body: ""}, {name: "deploy/a/2", body: ""}, {name: "deploy/a/3", body: ""}, {name: "deploy/a/4", body: ""}, {name: "deploy/a/5", body: ""}},
		"no vars":           {{name: "deploy/machine-files/a", body: "x"}},
		"unknown vars key":  {{name: "deploy/vars", body: "HOSTNAME=x\nNOT_A_BOOTY_KEY=secret-value\n"}},
		"bad vars line":     {{name: "deploy/vars", body: "HOSTNAME=x\nnot a line with secret-value\n"}},
	} {
		t.Run(name, func(t *testing.T) {
			root := useDeployRoot(t)
			c := &Client{vars: []byte(baseAgentVars)}
			_, _, err := c.CommandConfig(deployArchive(t, entries...))
			if err == nil {
				t.Fatal("unsafe archive applied")
			}
			if strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("error leaks a value: %v", err)
			}
			if b, err := os.ReadFile(filepath.Join(root, "machine-files/old")); err != nil || string(b) != "old" {
				t.Fatalf("/deploy changed by a rejected archive: %q, %v", b, err)
			}
		})
	}
}

// At the limits the archive still applies (the guards are not off by one).
func TestDeployArchiveAtTheLimits(t *testing.T) {
	defer func(b int64, n int) { maxDeployArchive, maxArchiveEntries = b, n }(maxDeployArchive, maxArchiveEntries)
	maxDeployArchive, maxArchiveEntries = 64, 3
	useDeployRoot(t)
	vars := "HOSTNAME=worker-7\n"
	payload := deployArchive(t,
		archiveEntry{name: "deploy/vars", body: vars},
		archiveEntry{name: "deploy/machine-files/a", body: strings.Repeat("x", 64-len(vars))},
		archiveEntry{name: "deploy/", typ: tar.TypeDir},
	)
	if _, _, err := (&Client{vars: []byte(baseAgentVars)}).CommandConfig(payload); err != nil {
		t.Fatalf("archive at the limits rejected: %v", err)
	}
}

func TestIsDeployArchive(t *testing.T) {
	if !isDeployArchive([]byte{0x1f, 0x8b, 8}) || isDeployArchive([]byte("HOSTNAME=x\n")) {
		t.Fatal("isDeployArchive does not tell gzip from vars lines")
	}
}
