package oci

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePlatform(t *testing.T) {
	tests := []struct {
		input   string
		os      string
		arch    string
		variant string
	}{
		{"linux/amd64", "linux", "amd64", ""},
		{"linux/arm64", "linux", "arm64", ""},
		{"linux/arm64/v8", "linux", "arm64", "v8"},
		{"linux", "linux", "amd64", ""},
	}

	for _, tc := range tests {
		p := ParsePlatform(tc.input)
		if p.OS != tc.os {
			t.Errorf("ParsePlatform(%q).OS = %q, want %q", tc.input, p.OS, tc.os)
		}
		if p.Architecture != tc.arch {
			t.Errorf("ParsePlatform(%q).Architecture = %q, want %q", tc.input, p.Architecture, tc.arch)
		}
		if p.Variant != tc.variant {
			t.Errorf("ParsePlatform(%q).Variant = %q, want %q", tc.input, p.Variant, tc.variant)
		}
	}
}

func TestMatchesPlatform(t *testing.T) {
	m := New(nil, &Config{Platform: "linux/amd64"})

	if !m.MatchesPlatform(Platform{OS: "linux", Architecture: "amd64"}) {
		t.Error("should match linux/amd64")
	}
	if m.MatchesPlatform(Platform{OS: "linux", Architecture: "arm64"}) {
		t.Error("should not match linux/arm64")
	}
	if m.MatchesPlatform(Platform{OS: "windows", Architecture: "amd64"}) {
		t.Error("should not match windows/amd64")
	}
}

func TestMatchesPlatform_WithVariant(t *testing.T) {
	m := New(nil, &Config{Platform: "linux/arm64/v8"})
	if !m.MatchesPlatform(Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}) {
		t.Error("should match with variant")
	}
	if m.MatchesPlatform(Platform{OS: "linux", Architecture: "arm64", Variant: "v7"}) {
		t.Error("should not match different variant")
	}
}

func TestCacheOperations(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, &Config{CacheDir: dir})

	// Initially not cached.
	if m.IsCached("sha256:abc") {
		t.Error("should not be cached initially")
	}

	// Add to cache.
	err := m.AddToCache("sha256:abc", "/layers/abc.tar", 1024)
	if err != nil {
		t.Fatalf("AddToCache: %v", err)
	}

	// Now should be cached.
	if !m.IsCached("sha256:abc") {
		t.Error("should be cached after add")
	}

	// Verify index file exists.
	indexPath := filepath.Join(dir, "index.json")
	if _, err := os.Stat(indexPath); err != nil {
		t.Errorf("index.json not found: %v", err)
	}
}

func TestNew_Defaults(t *testing.T) {
	m := New(nil, &Config{})
	if m.CacheDir() != "/mnt/target/.booty-cache/oci" {
		t.Errorf("default cacheDir = %q", m.CacheDir())
	}
}

func TestNew_CustomCacheDir(t *testing.T) {
	m := New(nil, &Config{CacheDir: "/custom/cache"})
	if m.CacheDir() != "/custom/cache" {
		t.Errorf("cacheDir = %q", m.CacheDir())
	}
}

func TestLayerInfo_Types(t *testing.T) {
	layer := LayerInfo{
		Digest:    "sha256:abc123",
		Size:      4096,
		MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
	}
	if layer.Digest != "sha256:abc123" {
		t.Error("wrong digest")
	}
}

func TestImageManifest_Types(t *testing.T) {
	manifest := ImageManifest{
		SchemaVersion: 2,
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
		Layers: []LayerInfo{
			{Digest: "sha256:layer1", Size: 100},
		},
	}
	if len(manifest.Layers) != 1 {
		t.Error("wrong layer count")
	}
}
