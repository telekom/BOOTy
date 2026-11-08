// Package labimages reads the external images of the e2e labs from the
// Dockerfiles in this directory, which exist only so Dependabot tracks them.
// test/e2e/clab/lab-images.sh does the same for shell callers.
package labimages

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

//go:embed Dockerfile */Dockerfile
var files embed.FS

var pinned = regexp.MustCompile(`^[^\s@]+:[^\s@:/]+@sha256:[0-9a-f]{64}$`)

// Images returns the image reference (name:tag@sha256:digest) of every stage.
func Images() (map[string]string, error) {
	paths, err := fs.Glob(files, "*/Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("glob lab image Dockerfiles: %w", err)
	}
	images := map[string]string{}
	for _, path := range append([]string{"Dockerfile"}, paths...) {
		data, err := files.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) == 0 || f[0] != "FROM" {
				continue
			}
			if len(f) != 4 || !strings.EqualFold(f[2], "AS") || !pinned.MatchString(f[1]) {
				return nil, fmt.Errorf("%s:%d: want FROM <image>:<tag>@sha256:<digest> AS <stage>", path, i+1)
			}
			if _, dup := images[f[3]]; dup {
				return nil, fmt.Errorf("%s:%d: duplicate stage %q", path, i+1, f[3])
			}
			images[f[3]] = f[1]
		}
	}
	return images, nil
}

// Image returns the image reference of stage.
func Image(stage string) (string, error) {
	images, err := Images()
	if err != nil {
		return "", err
	}
	image, ok := images[stage]
	if !ok {
		return "", fmt.Errorf("no lab image stage %q in test/e2e/clab/labimages", stage)
	}
	return image, nil
}

// EnvVar returns the variable that lab-images.sh sets to the image of stage
// and the topologies use as ${VAR}.
func EnvVar(stage string) string {
	return "BOOTY_LAB_" + strings.ToUpper(strings.ReplaceAll(stage, "-", "_")) + "_IMAGE"
}
