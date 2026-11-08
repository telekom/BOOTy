package labimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	imageLine = regexp.MustCompile(`(?m)^\s*image:\s*(\S+)`)
	labVar    = regexp.MustCompile(`^\$\{(BOOTY_LAB_[A-Z0-9_]+)\}$`)
	// ${VAR:=default} or ${VAR:-default}: the default must be a local image.
	defaulted = regexp.MustCompile(`^\$\{[A-Za-z0-9_]+:?[=-](.*)\}$`)
)

// TestTopologiesUseTrackedImages keeps every external topology image in the
// Dockerfiles here, where Dependabot tracks it: a topology uses either
// ${BOOTY_LAB_<STAGE>_IMAGE} for a stage here or a locally built booty-* image.
func TestTopologiesUseTrackedImages(t *testing.T) {
	images, err := Images()
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]bool{}
	for stage := range images {
		vars[EnvVar(stage)] = true
	}
	topologies, err := filepath.Glob("../*.clab.yml")
	if err != nil || len(topologies) == 0 {
		t.Fatalf("no topologies found: %v", err)
	}
	for _, topo := range topologies {
		data, err := os.ReadFile(topo)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range imageLine.FindAllStringSubmatch(string(data), -1) {
			image := m[1]
			if v := labVar.FindStringSubmatch(image); v != nil {
				if !vars[v[1]] {
					t.Errorf("%s: %s has no stage in test/e2e/clab/labimages", topo, image)
				}
				continue
			}
			if d := defaulted.FindStringSubmatch(image); d != nil {
				image = d[1]
			}
			if !strings.HasPrefix(image, "booty-") {
				t.Errorf("%s: image %s is not tracked by Dependabot; add a stage to test/e2e/clab/labimages/Dockerfile and use ${BOOTY_LAB_<STAGE>_IMAGE}", topo, m[1])
			}
		}
	}
}

// TestShellHelperMatches checks that lab-images.sh exports the same images.
func TestShellHelperMatches(t *testing.T) {
	images, err := Images()
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "bash", "../lab-images.sh", "env").Output()
	if err != nil {
		t.Fatalf("lab-images.sh: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && strings.HasPrefix(k, "BOOTY_LAB_") {
			got[k] = v
		}
	}
	if len(got) != len(images) {
		t.Errorf("lab-images.sh exports %d images, want %d: %v", len(got), len(images), got)
	}
	for stage, image := range images {
		if got[EnvVar(stage)] != image {
			t.Errorf("lab-images.sh: %s=%q, want %q", EnvVar(stage), got[EnvVar(stage)], image)
		}
	}
}

func TestImage(t *testing.T) {
	image, err := Image("frr")
	if err != nil || !strings.HasPrefix(image, "quay.io/frrouting/frr:") {
		t.Fatalf("Image(frr) = %q, %v", image, err)
	}
	if _, err := Image("missing"); err == nil {
		t.Fatal("Image(missing) succeeded")
	}
}

// TestShellHelperRejectsMalformed checks that lab-images.sh rejects the FROM
// lines that Images rejects, naming the file and line.
func TestShellHelperRejectsMalformed(t *testing.T) {
	script, err := os.ReadFile("../lab-images.sh")
	if err != nil {
		t.Fatal(err)
	}
	const digest = "f90d26a9fd5c14fc5795a73b4254ac88bc3186c45bbeb220a225fb6182de812c"
	for name, from := range map[string]string{
		"bad digest":   "quay.io/frrouting/frr:10.3.1@sha256:oops",
		"short digest": "quay.io/frrouting/frr:10.3.1@sha256:" + digest[:63],
		"upper digest": "quay.io/frrouting/frr:10.3.1@sha256:" + strings.ToUpper(digest),
		"no tag":       "quay.io/frrouting/frr@sha256:" + digest,
		"no digest":    "quay.io/frrouting/frr:10.3.1",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "labimages", "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			for path, content := range map[string]string{
				"lab-images.sh":            string(script),
				"labimages/Dockerfile":     "FROM " + from + " AS frr\n",
				"labimages/sub/Dockerfile": "FROM alpine:3.20@sha256:" + digest + " AS alpine\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out, err := exec.CommandContext(t.Context(), "bash", filepath.Join(dir, "lab-images.sh"), "true").CombinedOutput()
			if err == nil {
				t.Fatalf("lab-images.sh accepted FROM %s", from)
			}
			if !strings.Contains(string(out), "labimages/Dockerfile:1: want FROM") {
				t.Errorf("lab-images.sh output = %q, want file and line", out)
			}
		})
	}
}
