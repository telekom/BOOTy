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
