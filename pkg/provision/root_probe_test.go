//go:build linux

package provision

import (
	"os"
	"path/filepath"
	"testing"
)

// Legacy evidence: provision.sh is_root_partition_mount accepts a filesystem
// with /etc/os-release, or with /etc and /usr directories.
func TestLooksLikeRootFilesystem(t *testing.T) {
	tests := []struct {
		name  string
		dirs  []string
		files []string
		want  bool
	}{
		{name: "os-release", files: []string{"etc/os-release"}, want: true},
		{name: "etc and usr", dirs: []string{"etc", "usr"}, want: true},
		{name: "only etc", dirs: []string{"etc"}},
		{name: "data partition", files: []string{"data/file"}},
		{name: "os-release directory", dirs: []string{"etc/os-release"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, d := range tt.dirs {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range tt.files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := looksLikeRootFilesystem(dir); got != tt.want {
				t.Fatalf("looksLikeRootFilesystem() = %v, want %v", got, tt.want)
			}
		})
	}
}
