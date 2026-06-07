//go:build linux

package provision

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/telekom/BOOTy/pkg/config"
	imageutil "github.com/telekom/BOOTy/pkg/image"
)

const (
	defaultSysextCatalogDir = "/usr/lib/tcaas-sysext/preloaded"
	defaultSysextActiveDir  = "/var/lib/extensions"
	sysextModePreload       = "preload"
	sysextModeActive        = "active"
)

var sysextHTTPClient = &http.Client{
	Timeout: 30 * time.Minute,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: (&net.Dialer{
			Timeout: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

type sysextCatalog struct {
	APIVersion string               `json:"apiVersion"`
	Kind       string               `json:"kind"`
	Layers     []sysextCatalogLayer `json:"layers"`
}

type sysextCatalogLayer struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	FileName string `json:"fileName"`
	Path     string `json:"path"`
	Digest   string `json:"digest,omitempty"`
}

// ApplySysexts loads configured sysext artifacts into the provisioned root.
func (c *Configurator) ApplySysexts(ctx context.Context, cfg *config.SysextConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if len(cfg.Layers) == 0 {
		slog.Info("sysext provisioning enabled with no layers")
		return nil
	}

	catalog := &sysextCatalog{
		APIVersion: "imagebuilding.tcaas.telekom.de/v1alpha1",
		Kind:       "SysextPreloadCatalog",
	}
	if sysextHasPreloadLayers(cfg) {
		var err error
		catalog, err = c.readSysextCatalog(cfg)
		if err != nil {
			return err
		}
	}
	for i := range cfg.Layers {
		if err := c.applySysextLayer(ctx, cfg, &cfg.Layers[i], catalog); err != nil {
			return err
		}
	}
	return c.writeSysextCatalog(cfg, catalog)
}

func sysextHasPreloadLayers(cfg *config.SysextConfig) bool {
	for i := range cfg.Layers {
		if sysextLayerMode(cfg, &cfg.Layers[i]) == sysextModePreload {
			return true
		}
	}
	return false
}

func (c *Configurator) applySysextLayer(ctx context.Context, cfg *config.SysextConfig, layer *config.SysextLayerConfig, catalog *sysextCatalog) error {
	mode := sysextLayerMode(cfg, layer)
	fileName := sysextFileName(layer)
	targetDir, err := sysextTargetDir(cfg, mode)
	if err != nil {
		return fmt.Errorf("sysext %s: %w", layer.Name, err)
	}
	target, imagePath, err := sysextTargetPath(c.rootDir, targetDir, fileName)
	if err != nil {
		return fmt.Errorf("sysext %s target: %w", layer.Name, err)
	}

	digest, err := copySysextSource(ctx, layer.Source, target, layer.SHA256)
	if err != nil {
		return fmt.Errorf("sysext %s: %w", layer.Name, err)
	}
	slog.Info("sysext layer loaded", "name", layer.Name, "mode", mode, "path", imagePath)

	if mode == sysextModePreload {
		catalog.upsert(&sysextCatalogLayer{
			Name:     layer.Name,
			Version:  sysextVersion(layer),
			FileName: fileName,
			Path:     imagePath,
			Digest:   "sha256:" + digest,
		})
	}
	return nil
}

func (c *Configurator) readSysextCatalog(cfg *config.SysextConfig) (*sysextCatalog, error) {
	catalog := &sysextCatalog{
		APIVersion: "imagebuilding.tcaas.telekom.de/v1alpha1",
		Kind:       "SysextPreloadCatalog",
	}
	catalogPath, _, err := sysextTargetPath(c.rootDir, sysextCatalogDir(cfg), "catalog.json")
	if err != nil {
		return nil, fmt.Errorf("sysext catalog path: %w", err)
	}
	data, err := os.ReadFile(catalogPath) //nolint:gosec // path constrained to provisioned root
	if os.IsNotExist(err) {
		return catalog, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sysext catalog: %w", err)
	}
	if err := json.Unmarshal(data, catalog); err != nil {
		return nil, fmt.Errorf("parse sysext catalog: %w", err)
	}
	return catalog, nil
}

func (c *Configurator) writeSysextCatalog(cfg *config.SysextConfig, catalog *sysextCatalog) error {
	if len(catalog.Layers) == 0 {
		return nil
	}
	catalogPath, _, err := sysextTargetPath(c.rootDir, sysextCatalogDir(cfg), "catalog.json")
	if err != nil {
		return fmt.Errorf("sysext catalog path: %w", err)
	}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sysext catalog: %w", err)
	}
	data = append(data, '\n')
	if err := writeFileAtomic(catalogPath, data, 0o644); err != nil {
		return fmt.Errorf("write sysext catalog: %w", err)
	}
	return nil
}

func writeFileAtomic(target string, data []byte, perm os.FileMode) error {
	out, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*.tmp") //nolint:gosec // target directory constrained by caller
	if err != nil {
		return fmt.Errorf("create target: %w", err)
	}
	tmp := out.Name()
	if err := out.Chmod(perm); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp) //nolint:gosec // tmp was created next to target
		return fmt.Errorf("chmod target: %w", err)
	}
	if _, err := out.Write(data); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp) //nolint:gosec // tmp was created next to target
		return fmt.Errorf("write target: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp) //nolint:gosec // tmp was created next to target
		return fmt.Errorf("close target: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil { //nolint:gosec // atomic replacement within constrained target directory
		_ = os.Remove(tmp) //nolint:gosec // tmp was created next to target
		return fmt.Errorf("install target: %w", err)
	}
	return nil
}

func (c *sysextCatalog) upsert(layer *sysextCatalogLayer) {
	for i, existing := range c.Layers {
		if existing.Name == layer.Name && existing.Version == layer.Version && existing.FileName == layer.FileName {
			c.Layers[i] = *layer
			return
		}
	}
	c.Layers = append(c.Layers, *layer)
}

func sysextLayerMode(cfg *config.SysextConfig, layer *config.SysextLayerConfig) string {
	if mode := strings.ToLower(strings.TrimSpace(layer.Mode)); mode != "" {
		return mode
	}
	if mode := strings.ToLower(strings.TrimSpace(cfg.DefaultMode)); mode != "" {
		return mode
	}
	return sysextModePreload
}

func sysextTargetDir(cfg *config.SysextConfig, mode string) (string, error) {
	switch mode {
	case sysextModeActive:
		if cfg.ActiveDir != "" {
			return cfg.ActiveDir, nil
		}
		return defaultSysextActiveDir, nil
	case sysextModePreload:
		return sysextCatalogDir(cfg), nil
	default:
		return "", fmt.Errorf("invalid sysext mode %q", mode)
	}
}

func sysextCatalogDir(cfg *config.SysextConfig) string {
	if cfg.CatalogDir != "" {
		return cfg.CatalogDir
	}
	return defaultSysextCatalogDir
}

func sysextVersion(layer *config.SysextLayerConfig) string {
	if strings.TrimSpace(layer.Version) != "" {
		return strings.TrimSpace(layer.Version)
	}
	return "unknown"
}

func sysextFileName(layer *config.SysextLayerConfig) string {
	if strings.TrimSpace(layer.FileName) != "" {
		return strings.TrimSpace(layer.FileName)
	}
	if imageutil.IsOCIReference(layer.Source) {
		return strings.TrimSpace(layer.Name) + ".raw"
	}
	if name := sourceBaseName(layer.Source); name != "" && name != "." && name != "/" {
		return name
	}
	return strings.TrimSpace(layer.Name) + ".raw"
}

func sourceBaseName(source string) string {
	if u, err := url.Parse(source); err == nil && u.Scheme != "" {
		return path.Base(u.Path)
	}
	return filepath.Base(source)
}

func sysextTargetPath(root, dir, fileName string) (hostPath, imagePath string, err error) {
	if !isPlainFileName(fileName) {
		return "", "", fmt.Errorf("unsafe fileName %q", fileName)
	}
	cleanDir := path.Clean("/" + strings.TrimSpace(dir))
	if cleanDir == "/" {
		return "", "", fmt.Errorf("target directory must not be root")
	}
	imagePath = path.Join(cleanDir, fileName)
	hostPath = filepath.Join(root, strings.TrimPrefix(filepath.FromSlash(imagePath), string(filepath.Separator)))
	if err := ensureWithinRoot(root, hostPath); err != nil {
		return "", "", err
	}
	if err := ensureTargetParentWithinRoot(root, filepath.Dir(hostPath)); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return "", "", fmt.Errorf("create target directory: %w", err)
	}
	if err := ensureTargetParentWithinRoot(root, filepath.Dir(hostPath)); err != nil {
		return "", "", err
	}
	return hostPath, imagePath, nil
}

func isPlainFileName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.Contains(name, "/") &&
		!strings.Contains(name, "\\") &&
		!strings.Contains(name, "..")
}

func ensureWithinRoot(root, target string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}
	if absTarget != absRoot && !strings.HasPrefix(absTarget, absRoot+string(filepath.Separator)) {
		return fmt.Errorf("target escapes provisioned root: %s", target)
	}
	return nil
}

func ensureTargetParentWithinRoot(root, parent string) error {
	existing := parent
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect target directory: %w", err)
		}
		next := filepath.Dir(existing)
		if next == existing {
			return fmt.Errorf("target parent has no existing ancestor: %s", parent)
		}
		existing = next
	}
	realExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return fmt.Errorf("resolve target directory symlinks: %w", err)
	}
	return ensureWithinRoot(root, realExisting)
}

func copySysextSource(ctx context.Context, source, target, expected string) (string, error) {
	src, err := openSysextSource(ctx, source)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()

	out, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*.tmp") //nolint:gosec // target directory constrained to provisioned root
	if err != nil {
		return "", fmt.Errorf("create target: %w", err)
	}
	tmp := out.Name()
	if err := out.Chmod(0o644); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp) //nolint:gosec // tmp was created in the constrained sysext target directory
		return "", fmt.Errorf("chmod target: %w", err)
	}
	digest, copyErr := writeAndHash(ctx, src, out)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp) //nolint:gosec // tmp was created in the constrained sysext target directory
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp) //nolint:gosec // tmp was created in the constrained sysext target directory
		return "", fmt.Errorf("close target: %w", closeErr)
	}
	if err := verifySysextDigest(digest, expected); err != nil {
		_ = os.Remove(tmp) //nolint:gosec // tmp was created in the constrained sysext target directory
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil { //nolint:gosec // both paths are in the constrained sysext target directory
		_ = os.Remove(tmp) //nolint:gosec // tmp was created in the constrained sysext target directory
		return "", fmt.Errorf("install target: %w", err)
	}
	return digest, nil
}

func openSysextSource(ctx context.Context, source string) (io.ReadCloser, error) {
	if imageutil.IsOCIReference(source) {
		ref := imageutil.TrimOCIScheme(source)
		slog.Info("pulling OCI sysext layer", "ref", ref)
		return imageutil.FetchOCILayerWithRetry(ctx, ref)
	}
	u, err := url.Parse(source)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("create sysext request: %w", err)
		}
		resp, err := sysextHTTPClient.Do(req) //nolint:gosec // configured sysext source URL
		if err != nil {
			return nil, fmt.Errorf("fetch sysext: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("fetch sysext: HTTP %d", resp.StatusCode)
		}
		return resp.Body, nil
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, fmt.Errorf("stat sysext source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("sysext source must be a regular file: %s", source)
	}
	file, err := os.Open(source) //nolint:gosec // configured source path
	if err != nil {
		return nil, fmt.Errorf("open sysext source: %w", err)
	}
	return file, nil
}

func writeAndHash(ctx context.Context, src io.Reader, dst io.Writer) (string, error) {
	hash := sha256.New()
	buf := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("copy sysext canceled: %w", err)
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if _, err := hash.Write(chunk); err != nil {
				return "", fmt.Errorf("hash sysext: %w", err)
			}
			written, err := dst.Write(chunk)
			if err != nil {
				return "", fmt.Errorf("write sysext: %w", err)
			}
			if written != len(chunk) {
				return "", io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return hex.EncodeToString(hash.Sum(nil)), nil
		}
		if readErr != nil {
			return "", fmt.Errorf("read sysext: %w", readErr)
		}
	}
}

func verifySysextDigest(got, expected string) error {
	expected = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(expected)), "sha256:")
	if expected == "" {
		return nil
	}
	if got != expected {
		return fmt.Errorf("sysext digest mismatch: got sha256:%s want sha256:%s", got, expected)
	}
	return nil
}
