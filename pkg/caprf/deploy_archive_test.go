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
	dir              bool
}

func deployArchive(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		switch {
		case e.dir:
			hdr.Typeflag, hdr.Mode, hdr.Size = tar.TypeDir, 0o755, 0
		case e.link != "":
			hdr.Typeflag, hdr.Linkname, hdr.Size = tar.TypeSymlink, e.link, 0
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
	"HEARTBEAT_URL=https://caprf/agent/heartbeat\nCOMMANDS_URL=https://caprf/agent/commands\nHOSTNAME=old\n"

// The archive of the next job replaces the per-job files and vars; the
// agent keeps its token and endpoints.
func TestDeployArchiveConfigReplacesJobFiles(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func(prev string) func() { return func() { deployRoot = prev } }(deployRoot))
	deployRoot = root
	for _, f := range []string{"machine-files/old", "machine-commands/old.sh", "file-system/etc/old"} {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	payload := deployArchive(t,
		archiveEntry{name: "deploy/", dir: true},
		archiveEntry{name: "deploy/vars", body: "TOKEN=job-token\nMODE=provision\nINIT_URL=https://caprf/status/init\nHOSTNAME=worker-7\n"},
		archiveEntry{name: "deploy/machine-files/etc/cloud/nocloud/user-data", body: "#cloud-config\n"},
		archiveEntry{name: "deploy/machine-files/etc/localtime", link: "/usr/share/zoneinfo/UTC"},
	)

	c := &Client{vars: []byte(baseAgentVars)}
	cfg, keys, err := c.CommandConfig(payload)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hostname != "worker-7" || cfg.Transport.Token != "agent-token" || cfg.Mode != "standby" {
		t.Fatalf("hostname %q token %q mode %q, want the archive hostname and the agent token and mode", cfg.Hostname, cfg.Transport.Token, cfg.Mode)
	}
	if strings.Join(keys, ",") != "HOSTNAME,INIT_URL,MODE,TOKEN" {
		t.Fatalf("keys = %v", keys)
	}
	if b, err := os.ReadFile(filepath.Join(root, "machine-files/etc/cloud/nocloud/user-data")); err != nil || string(b) != "#cloud-config\n" {
		t.Fatalf("user-data = %q, %v", b, err)
	}
	if l, err := os.Readlink(filepath.Join(root, "machine-files/etc/localtime")); err != nil || l != "/usr/share/zoneinfo/UTC" {
		t.Fatalf("symlink = %q, %v", l, err)
	}
	for _, gone := range []string{"machine-files/old", "machine-commands", "file-system"} {
		if _, err := os.Lstat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s of the previous job still exists (%v)", gone, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(root, ".archive-*")); len(left) != 0 {
		t.Fatalf("staging left behind: %v", left)
	}
}

func TestDeployArchiveRejectsUnsafeEntries(t *testing.T) {
	for name, entries := range map[string][]archiveEntry{
		"outside deploy": {{name: "etc/passwd", body: "x"}},
		"dot dot":        {{name: "deploy/../etc/passwd", body: "x"}},
		"below symlink":  {{name: "deploy/machine-files/l", link: "/etc"}, {name: "deploy/machine-files/l/passwd", body: "x"}},
		"no vars":        {{name: "deploy/machine-files/a", body: "x"}},
		"device":         {{name: "deploy/vars", body: "HOSTNAME=x\n"}, {name: "deploy/machine-files/dev", body: ""}},
	} {
		t.Run(name, func(t *testing.T) {
			payload := deployArchive(t, entries...)
			if name == "device" {
				payload = deviceArchive(t)
			}
			if _, err := applyDeployArchive(payload, t.TempDir()); err == nil {
				t.Fatal("unsafe archive applied")
			}
		})
	}
}

func deviceArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "deploy/vars", Mode: 0o644, Size: 0, Typeflag: tar.TypeReg})
	_ = tw.WriteHeader(&tar.Header{Name: "deploy/machine-files/null", Mode: 0o644, Typeflag: tar.TypeChar})
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestIsDeployArchive(t *testing.T) {
	if !isDeployArchive([]byte{0x1f, 0x8b, 8}) || isDeployArchive([]byte("HOSTNAME=x\n")) {
		t.Fatal("isDeployArchive does not tell gzip from vars lines")
	}
}
