package oci

import (
	"os"
	"path/filepath"
	"runtime"
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
		// No arch specified: should default to runtime.GOARCH.
		{"linux", "linux", runtime.GOARCH, ""},
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
	if cached, err := m.IsCached("sha256:abc"); err != nil {
		t.Fatalf("IsCached: %v", err)
	} else if cached {
		t.Error("should not be cached initially")
	}

	// Add to cache.
	err := m.AddToCache("sha256:abc", "/layers/abc.tar", 1024)
	if err != nil {
		t.Fatalf("AddToCache: %v", err)
	}

	// Now should be cached.
	if cached, err := m.IsCached("sha256:abc"); err != nil {
		t.Fatalf("IsCached: %v", err)
	} else if !cached {
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

func TestNew_NilConfig(t *testing.T) {
	m := New(nil, nil)
	if m.CacheDir() != "/mnt/target/.booty-cache/oci" {
		t.Errorf("nil cfg cacheDir = %q", m.CacheDir())
	}
	if m.platform.OS != "linux" {
		t.Errorf("nil cfg OS = %q", m.platform.OS)
	}
}

func TestNew_CustomCacheDir(t *testing.T) {
	m := New(nil, &Config{CacheDir: "/custom/cache"})
	if m.CacheDir() != "/custom/cache" {
		t.Errorf("cacheDir = %q", m.CacheDir())
	}
}

func TestParsePlatform_EmptySegments(t *testing.T) {
	tests := []struct {
		name  string
		input string
		os    string
		arch  string
	}{
		{"empty string", "", "linux", runtime.GOARCH},
		{"trailing slash", "linux/", "linux", runtime.GOARCH},
		{"leading slash", "/amd64", "linux", "amd64"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := ParsePlatform(tc.input)
			if p.OS != tc.os {
				t.Errorf("ParsePlatform(%q).OS = %q, want %q", tc.input, p.OS, tc.os)
			}
			if p.Architecture != tc.arch {
				t.Errorf("ParsePlatform(%q).Architecture = %q, want %q", tc.input, p.Architecture, tc.arch)
			}
		})
	}
}

func TestAddToCache_Validation(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, &Config{CacheDir: dir})

	if err := m.AddToCache("", "/path", 100); err == nil {
		t.Error("expected error for empty digest")
	}
	if err := m.AddToCache("sha256:abc", "", 100); err == nil {
		t.Error("expected error for empty path")
	}
	if err := m.AddToCache("sha256:abc", "/path", -1); err == nil {
		t.Error("expected error for negative size")
	}
	// Zero size is valid (empty layer).
	if err := m.AddToCache("sha256:abc", "/path", 0); err != nil {
		t.Errorf("zero size should be valid: %v", err)
	}
}
