//go:build linux

// Package espsync mirrors a mounted EFI system partition to its peer.
package espsync

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/unix"
)

const defaultDebounce = 750 * time.Millisecond

const stagingPrefix = ".booty-esp-sync-staging-"

// CopyTree makes destination an exact, safe copy of source. It stages every
// regular file beside its final name and only removes stale destination paths
// after the source walk succeeds.
func CopyTree(source, destination string) error {
	source, destination, err := validateRoots(source, destination)
	if err != nil {
		return err
	}
	if err := cleanStaging(destination); err != nil {
		return fmt.Errorf("clean stale staging: %w", err)
	}
	// Keep staging on the destination filesystem. ESP destinations are often
	// mounted VFAT filesystems, while their parent (/run) is a different
	// filesystem; a sibling staging directory would make Rename return EXDEV.
	staged, err := os.MkdirTemp(destination, stagingPrefix)
	if err != nil {
		return fmt.Errorf("create staging: %w", err)
	}
	defer func() { _ = os.RemoveAll(staged) }()
	if err := copyDir(source, staged); err != nil {
		if cleanupErr := os.RemoveAll(staged); cleanupErr != nil {
			return fmt.Errorf("copy source: %w (cleanup staging: %v)", err, cleanupErr)
		}
		return err
	}
	if err := syncTree(staged, destination); err != nil {
		if cleanupErr := os.RemoveAll(staged); cleanupErr != nil {
			return fmt.Errorf("sync destination: %w (cleanup staging: %v)", err, cleanupErr)
		}
		return err
	}
	if err := os.RemoveAll(staged); err != nil {
		return fmt.Errorf("remove staging: %w", err)
	}
	return nil
}

func cleanStaging(destination string) error {
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !isStagingName(entry.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func isStagingName(name string) bool {
	return strings.HasPrefix(name, stagingPrefix)
}

func validateRoots(source, destination string) (string, string, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return "", "", err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return "", "", err
	}
	if source == destination {
		return "", "", errors.New("source and destination are identical")
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return "", "", fmt.Errorf("stat source: %w", err)
	}
	destinationInfo, err := os.Stat(destination)
	if err != nil {
		return "", "", fmt.Errorf("stat destination: %w", err)
	}
	if !sourceInfo.IsDir() || !destinationInfo.IsDir() {
		return "", "", errors.New("source and destination must be directories")
	}
	var sourceStat, destinationStat syscall.Stat_t
	if err := syscall.Stat(source, &sourceStat); err != nil {
		return "", "", err
	}
	if err := syscall.Stat(destination, &destinationStat); err != nil {
		return "", "", err
	}
	if sourceStat.Dev == destinationStat.Dev && sourceStat.Ino == destinationStat.Ino {
		return "", "", errors.New("source and destination resolve to the same directory")
	}
	return source, destination, nil
}

func copyDir(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if isStagingName(entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch info.Mode() & os.ModeType {
		case 0:
			if err := copyFile(path, target, info.Mode().Perm()); err != nil {
				return err
			}
		case os.ModeDir:
			return os.MkdirAll(target, info.Mode().Perm())
		case os.ModeSymlink:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return fmt.Errorf("unsupported ESP entry %s", path)
		}
		return nil
	})
}

func copyFile(source, destination string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, mode.Perm())
	if err != nil {
		return fmt.Errorf("create %s: %w", destination, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy %s: %w", source, err)
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func syncTree(staged, destination string) error {
	// Prune before moving staged entries. Moving a staged file would otherwise
	// make the staged manifest disappear and the subsequent prune would delete
	// the freshly installed destination file.
	if err := pruneTree(destination, staged); err != nil {
		return err
	}
	if err := filepath.WalkDir(staged, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(staged, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			if info, err := os.Lstat(target); err == nil && !info.IsDir() {
				if err := os.RemoveAll(target); err != nil {
					return err
				}
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}
			return os.MkdirAll(target, 0o755)
		}
		if err := replacePath(target, path); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func pruneTree(destination, staged string) error {
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(destination, entry.Name())
		if filepath.Clean(path) == filepath.Clean(staged) {
			continue
		}
		rel := entry.Name()
		stagedPath := filepath.Join(staged, rel)
		if _, err := os.Lstat(stagedPath); os.IsNotExist(err) {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove stale ESP path %s: %w", path, err)
			}
			continue
		} else if err != nil {
			return err
		}
		if entry.IsDir() && stagedEntryIsDir(stagedPath) {
			if err := pruneTree(path, stagedPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func stagedEntryIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func replacePath(destination, staged string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := os.Rename(staged, destination); err == nil {
		return nil
	} else if !isDirectory(destination) {
		return err
	}
	// A directory cannot be atomically replaced by a regular file or symlink
	// on POSIX/FAT. The normal path above remains rename-over-existing and
	// therefore never exposes a missing boot file for matching entry types.
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	return os.Rename(staged, destination)
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// RunOnce serializes a copy across service instances using flock.
func RunOnce(source, destination, lockPath string) error {
	return WithLock(lockPath, func() error {
		slog.Info("synchronizing EFI system partition", "source", source, "destination", destination)
		return CopyTree(source, destination)
	})
}

// WithLock serializes the complete mount, copy and unmount operation used by
// a dynamically resolved mirrored peer.
func WithLock(lockPath string, fn func() error) error {
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	return fn()
}

// ValidateMountedSource proves that source is backed by the expected ESP
// partition. It compares the mounted filesystem device with the block device
// behind the PARTUUID link, avoiding filesystem UUID ambiguity.
func ValidateMountedSource(source, partUUID string) error {
	var mounted, device unix.Stat_t
	if err := unix.Stat(source, &mounted); err != nil {
		return fmt.Errorf("stat mounted source: %w", err)
	}
	if err := unix.Stat("/dev/disk/by-partuuid/"+partUUID, &device); err != nil {
		return fmt.Errorf("stat source PARTUUID: %w", err)
	}
	if int64(mounted.Dev) != int64(device.Rdev) {
		return fmt.Errorf("mounted source device %d does not match PARTUUID device %d", mounted.Dev, device.Rdev)
	}
	return nil
}

// PeerDevice returns the other ESP in a mirrored pair. It resolves the
// mounted source device on every call so failover and remounts select the
// current peer rather than a provisioning-time path.
func PeerDevice(source, firstPartUUID, secondPartUUID string) (string, error) {
	var mounted unix.Stat_t
	if err := unix.Stat(source, &mounted); err != nil {
		return "", fmt.Errorf("stat mounted source: %w", err)
	}
	for _, pair := range [][2]string{{firstPartUUID, secondPartUUID}, {secondPartUUID, firstPartUUID}} {
		var device unix.Stat_t
		if err := unix.Stat("/dev/disk/by-partuuid/"+pair[0], &device); err != nil {
			continue
		}
		if int64(mounted.Dev) == int64(device.Rdev) {
			return "/dev/disk/by-partuuid/" + pair[1], nil
		}
	}
	return "", fmt.Errorf("mounted source does not match mirrored ESP pair %q or %q", firstPartUUID, secondPartUUID)
}

// Watch recursively observes source and debounces nested changes.
func Watch(source, destination, lockPath string, done <-chan struct{}) error {
	return WatchFunc(source, func() error { return RunOnce(source, destination, lockPath) }, done)
}

// WatchWithMarker behaves like Watch and writes marker after every successful
// synchronization. The marker is useful to boot integration tests and local
// operators because it records that the initial copy completed.
func WatchWithMarker(source, destination, lockPath, marker string, done <-chan struct{}) error {
	return WatchFuncWithMarker(source, func() error { return RunOnce(source, destination, lockPath) }, marker, done)
}

// WatchFunc recursively observes source and runs sync after each debounced
// change. The callback may resolve a destination dynamically for failover.
func WatchFunc(source string, sync func() error, done <-chan struct{}) error {
	return WatchFuncWithMarker(source, sync, "", done)
}

func WatchFuncWithMarker(source string, sync func() error, marker string, done <-chan struct{}) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = watcher.Close() }()
	if err := addDirs(watcher, source); err != nil {
		return err
	}
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	for {
		select {
		case <-done:
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
				_ = addDirs(watcher, event.Name)
			}
			resetTimer(timer, defaultDebounce)
		case err, ok := <-watcher.Errors:
			if ok {
				slog.Warn("EFI watcher error", "error", err)
			}
		case <-timer.C:
			if err := sync(); err != nil {
				slog.Error("EFI synchronization failed", "error", err)
			} else if err := writeMarker(marker); err != nil {
				slog.Error("write EFI synchronization marker", "error", err)
			}
		}
	}
}

func WriteMarker(marker string) error {
	return writeMarker(marker)
}

func writeMarker(marker string) error {
	if marker == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(marker), ".booty-esp-sync-marker-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := fmt.Fprintf(tmp, "%s\n", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, marker)
}

func addDirs(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return w.Add(path)
		}
		return nil
	})
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}
