package oci

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Manager handles OCI image operations for OS provisioning.
type Manager struct {
	log      *slog.Logger
	cacheDir string
	platform Platform
}

// Platform identifies the target platform for multi-arch images.
type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

// Config holds OCI image configuration.
type Config struct {
	Reference       string `json:"reference"`
	Platform        string `json:"platform"`
	VerifySignature bool   `json:"verifySignature"`
	CosignKey       string `json:"cosignKey,omitempty"`
	CacheEnabled    bool   `json:"cacheEnabled"`
	CacheDir        string `json:"cacheDir,omitempty"`
	Parallel        int    `json:"parallel"`
}

// LayerInfo describes a single OCI layer.
type LayerInfo struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType"`
}

// ImageManifest is a simplified OCI image manifest.
type ImageManifest struct {
	SchemaVersion int         `json:"schemaVersion"`
	MediaType     string      `json:"mediaType"`
	Config        LayerInfo   `json:"config"`
	Layers        []LayerInfo `json:"layers"`
}

// CacheEntry records a cached layer.
type CacheEntry struct {
	Digest    string    `json:"digest"`
	Size      int64     `json:"size"`
	CachedAt  time.Time `json:"cachedAt"`
	LocalPath string    `json:"localPath"`
}

// CacheIndex tracks all cached layers.
type CacheIndex struct {
	Entries map[string]CacheEntry `json:"entries"`
}

// New creates an OCI image manager.
func New(log *slog.Logger, cfg *Config) *Manager {
	p := Platform{OS: "linux", Architecture: runtime.GOARCH}
	if cfg.Platform != "" {
		p = ParsePlatform(cfg.Platform)
	}
	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		cacheDir = "/mnt/target/.booty-cache/oci"
	}
	return &Manager{log: log, cacheDir: cacheDir, platform: p}
}

// ParsePlatform parses "linux/amd64" or "linux/arm64/v8" into a Platform.
func ParsePlatform(s string) Platform {
	parts := strings.SplitN(s, "/", 3)
	p := Platform{OS: "linux", Architecture: runtime.GOARCH}
	if len(parts) >= 1 {
		p.OS = parts[0]
	}
	if len(parts) >= 2 {
		p.Architecture = parts[1]
	}
	if len(parts) >= 3 {
		p.Variant = parts[2]
	}
	return p
}

// CacheDir returns the configured cache directory.
func (m *Manager) CacheDir() string {
	return m.cacheDir
}

// IsCached checks if a layer digest is in the cache.
func (m *Manager) IsCached(digest string) (bool, error) {
	idx, err := m.loadCacheIndex()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("check cache for %s: %w", digest, err)
	}
	_, ok := idx.Entries[digest]
	return ok, nil
}

// AddToCache records a layer in the cache index.
func (m *Manager) AddToCache(digest, localPath string, size int64) error {
	idx, err := m.loadCacheIndex()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load cache index: %w", err)
		}
		if m.log != nil {
			m.log.Info("initializing new cache index")
		}
		idx = &CacheIndex{Entries: make(map[string]CacheEntry)}
	}
	idx.Entries[digest] = CacheEntry{
		Digest:    digest,
		Size:      size,
		CachedAt:  time.Now(),
		LocalPath: localPath,
	}
	return m.saveCacheIndex(idx)
}

func (m *Manager) loadCacheIndex() (*CacheIndex, error) {
	path := filepath.Join(m.cacheDir, "index.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cache index: %w", err)
	}
	var idx CacheIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse cache index: %w", err)
	}
	if idx.Entries == nil {
		idx.Entries = make(map[string]CacheEntry)
	}
	return &idx, nil
}

func (m *Manager) saveCacheIndex(idx *CacheIndex) error {
	if err := os.MkdirAll(m.cacheDir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache index: %w", err)
	}
	path := filepath.Join(m.cacheDir, "index.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write cache index: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit cache index: %w", err)
	}
	return nil
}

// MatchesPlatform checks if a platform matches the configured target.
func (m *Manager) MatchesPlatform(p Platform) bool {
	if p.OS != m.platform.OS {
		return false
	}
	if p.Architecture != m.platform.Architecture {
		return false
	}
	if m.platform.Variant != "" && p.Variant != m.platform.Variant {
		return false
	}
	return true
}
