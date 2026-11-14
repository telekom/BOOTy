//go:build linux

package provision

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/telekom/BOOTy/pkg/config"
	"github.com/telekom/BOOTy/pkg/secureboot"
	"github.com/telekom/BOOTy/pkg/verity"
)

// Experimental: MODULE_SIGNING_TARGET=sysext (docs/module-signing.md,
// dm-verity roots).
const (
	// moduleSysextName names the extension and its files in moduleSysextDir.
	moduleSysextName = "booty-modules"
	moduleSysextDir  = config.DefaultSysextActiveDir
	// moduleSysextPolicy lets systemd-sysext merge only images with a
	// signed dm-verity root hash, so a changed or unsigned image is refused.
	moduleSysextPolicy = "root=signed+absent:usr=signed+absent"
	// moduleSysextDropIn makes systemd-sysext.service apply the policy.
	moduleSysextDropIn = "etc/systemd/system/systemd-sysext.service.d/50-booty-modules.conf"
	moduleSysextWants  = "etc/systemd/system/sysinit.target.wants/systemd-sysext.service"
)

const moduleSysextDropInUnit = `# BOOTy (MODULE_SIGNING_TARGET=sysext): merge only system extensions whose
# dm-verity root hash signature the kernel accepts. systemd 255 asks for a
# passphrase when the kernel refuses the signature; the timeout keeps that
# from blocking the boot.
[Service]
TimeoutStartSec=90s
ExecStart=
ExecStart=/usr/bin/systemd-sysext refresh --image-policy=` + moduleSysextPolicy + "\n"

// signKernelModulesSysext signs copies of the out-of-tree modules into a
// per-host system extension and leaves /usr of the target unchanged, so a
// dm-verity protected /usr or root stays valid. The extension is an ext4
// image with a dm-verity hash tree; its root hash is signed with the
// per-host key before the key is destroyed, and the kernel checks that
// signature against the keyring that holds the enrolled MOK.
func (o *Orchestrator) signKernelModulesSysext(ctx context.Context, ms config.ModuleSigningConfig) error {
	root, err := os.OpenRoot(o.config.rootDir)
	if err != nil {
		return fmt.Errorf("open target root: %w", err)
	}
	defer func() { _ = root.Close() }()
	// /lib/modules must be /usr/lib/modules: an extension covers /usr only.
	target, err := root.Readlink("lib")
	if err != nil {
		return fmt.Errorf("target sysext needs a merged /usr (lib -> usr/lib): %w", err)
	}
	if path.Clean(target) != "usr/lib" {
		return fmt.Errorf("target sysext needs a merged /usr, lib links to %q", target)
	}
	osID, osVersion, err := targetOSRelease(root)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "booty-sysext-")
	if err != nil {
		return fmt.Errorf("sysext staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	image, hashTree := filepath.Join(stage, "image.raw"), filepath.Join(stage, "image.verity")
	tree := filepath.Join(stage, "tree")
	if err := os.Mkdir(tree, 0o755); err != nil {
		return fmt.Errorf("sysext staging directory: %w", err)
	}
	certPath := path.Join(ms.CertDirOrDefault(), moduleCertFile)
	var rootHash string
	res, err := secureboot.SignModulesInto(o.config.rootDir, tree, "BOOTy module signing "+o.cfg.Hostname,
		func(cert []byte) ([]byte, error) {
			release := "ID=" + osID + "\n"
			if osVersion != "" {
				release += "VERSION_ID=" + osVersion + "\n"
			}
			if err := writeStageFile(tree, "usr/lib/extension-release.d/extension-release."+moduleSysextName, []byte(release)); err != nil {
				return nil, err
			}
			if err := writeStageFile(tree, strings.TrimPrefix(certPath, "/"), cert); err != nil {
				return nil, err
			}
			if err := makeExt4Image(ctx, tree, image); err != nil {
				return nil, err
			}
			h, err := verity.Format(image, hashTree)
			rootHash = h
			return []byte(h), err //nolint:wrapcheck // verity errors name the step
		})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(res.CertDER)
	o.moduleSigning = &ReportModuleSigning{
		Modules: len(res.Modules), Enrollment: ms.EnrollmentMethod(), Target: config.ModuleSigningTargetSysext,
		CertPath: certPath, CertSHA256: hex.EncodeToString(sum[:]), Cert: res.CertDER,
	}
	if len(res.Modules) == 0 {
		o.log.Info("no out-of-tree kernel modules, no module sysext", "certSHA256", o.moduleSigning.CertSHA256)
		return nil
	}
	if err := installModuleSysext(root, image, hashTree, rootHash, res.SealSignature); err != nil {
		return err
	}
	o.moduleSigning.Sysext = path.Join(moduleSysextDir, moduleSysextName+".raw")
	o.moduleSigning.RootHash = rootHash
	o.log.Info("signed out-of-tree kernel modules into a system extension", "modules", len(res.Modules),
		"sysext", o.moduleSigning.Sysext, "rootHash", rootHash, "certSHA256", o.moduleSigning.CertSHA256)
	return nil
}

func writeStageFile(dir, rel string, data []byte) error {
	dst := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("sysext staging: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil { //nolint:gosec // public sysext content
		return fmt.Errorf("sysext staging: %w", err)
	}
	return nil
}

// makeExt4Image builds a read-only ext4 image of tree. mkfs.ext4 is in
// every ramdisk that ships efibootmgr; no journal, so a read-only mount of
// the verity device never needs to write.
// ponytail: ext4, not EROFS; mkfs.erofs would add erofs-utils to the ramdisk.
func makeExt4Image(ctx context.Context, tree, image string) error {
	var size int64
	err := filepath.WalkDir(tree, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info, err := d.Info(); err == nil {
			size += info.Size() + 4096
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("size sysext tree: %w", err)
	}
	// Inode tables and metadata: 25% plus 4 MiB, in whole 4 KiB blocks.
	size = (size*5/4 + 4<<20 + 4095) / 4096 * 4096
	f, err := os.Create(image) //nolint:gosec // staging file
	if err != nil {
		return fmt.Errorf("create sysext image: %w", err)
	}
	err = f.Truncate(size)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("size sysext image: %w", err)
	}
	out, err := hostCommandCombinedOutput(ctx, "mkfs.ext4", "-q", "-F", "-b", "4096", "-m", "0",
		"-O", "^has_journal,^orphan_file", "-E", "root_owner=0:0", "-L", moduleSysextName, "-d", tree, image)
	if err != nil {
		return fmt.Errorf("mkfs.ext4 sysext image: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// installModuleSysext writes the image with its sidecar files, which
// systemd-sysext reads next to <name>.raw: the hash tree (.verity), the
// root hash (.roothash) and its signature (.roothash.p7s). It also enables
// systemd-sysext.service with the signed-only image policy.
func installModuleSysext(root *os.Root, image, hashTree, rootHash string, sig []byte) error {
	dir := strings.TrimPrefix(moduleSysextDir, "/")
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", moduleSysextDir, err)
	}
	for ext, src := range map[string]string{".raw": image, ".verity": hashTree} {
		if err := copyIntoRoot(root, src, path.Join(dir, moduleSysextName+ext)); err != nil {
			return fmt.Errorf("install sysext %s: %w", ext, err)
		}
	}
	for ext, data := range map[string][]byte{".roothash": []byte(rootHash + "\n"), ".roothash.p7s": sig} {
		if err := root.WriteFile(path.Join(dir, moduleSysextName+ext), data, 0o644); err != nil {
			return fmt.Errorf("install sysext %s: %w", ext, err)
		}
	}
	if err := root.MkdirAll(path.Dir(moduleSysextDropIn), 0o755); err != nil {
		return fmt.Errorf("systemd-sysext drop-in: %w", err)
	}
	if err := root.WriteFile(moduleSysextDropIn, []byte(moduleSysextDropInUnit), 0o644); err != nil {
		return fmt.Errorf("systemd-sysext drop-in: %w", err)
	}
	if err := root.MkdirAll(path.Dir(moduleSysextWants), 0o755); err != nil {
		return fmt.Errorf("enable systemd-sysext: %w", err)
	}
	if err := root.Remove(moduleSysextWants); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("enable systemd-sysext: %w", err)
	}
	if err := root.Symlink("/usr/lib/systemd/system/systemd-sysext.service", moduleSysextWants); err != nil {
		return fmt.Errorf("enable systemd-sysext: %w", err)
	}
	return nil
}

// copyIntoRoot streams src to dst inside root: the image is already in RAM
// once, in the staging directory.
func copyIntoRoot(root *os.Root, src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // staging file
	if err != nil {
		return err //nolint:wrapcheck // the caller names the file
	}
	defer func() { _ = in.Close() }()
	out, err := root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err //nolint:wrapcheck // the caller names the file
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err //nolint:wrapcheck // the caller names the file
}

// targetOSRelease returns ID and VERSION_ID of the target, which
// systemd-sysext compares with extension-release. /etc/os-release takes
// precedence over /usr/lib/os-release, as for systemd.
func targetOSRelease(root *os.Root) (id, version string, err error) {
	data, err := root.ReadFile("etc/os-release")
	if err != nil {
		if data, err = root.ReadFile("usr/lib/os-release"); err != nil {
			return "", "", fmt.Errorf("read target os-release: %w", err)
		}
	}
	for sc := bufio.NewScanner(bytes.NewReader(data)); sc.Scan(); {
		k, v, ok := strings.Cut(sc.Text(), "=")
		v = strings.Trim(v, `"'`)
		switch {
		case !ok:
		case k == "ID":
			id = v
		case k == "VERSION_ID":
			version = v
		}
	}
	if !kernelReleasePattern.MatchString(id) || (version != "" && !kernelReleasePattern.MatchString(version)) {
		return "", "", fmt.Errorf("target os-release: unexpected ID %q or VERSION_ID %q", id, version)
	}
	return id, version, nil
}
