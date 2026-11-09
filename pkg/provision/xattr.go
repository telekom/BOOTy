//go:build linux

package provision

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"golang.org/x/sys/unix"
)

// copyXattrs copies every extended attribute of src to dst, without
// following symlinks. That includes POSIX ACLs (system.posix_acl_*), file
// capabilities (security.capability) and security labels. Set them after
// the owner and mode: chown drops security.capability and chmod rewrites
// the ACL mask.
//
// A POSIX ACL of dst that src does not have is removed: dst can inherit one
// from the default ACL that an interrupted earlier seed left on a parent.
func copyXattrs(src, dst string) error {
	names, err := listXattrs(src)
	if err != nil {
		return err
	}
	for _, acl := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		if slices.Contains(names, acl) {
			continue
		}
		err := unix.Lremovexattr(dst, acl)
		if err != nil && !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.EPERM) {
			return fmt.Errorf("remove xattr %s from %s: %w", acl, dst, err)
		}
	}
	for _, name := range names {
		value, err := getXattr(src, name)
		if errors.Is(err, unix.ENODATA) {
			continue // removed since the list
		}
		if err != nil {
			return err
		}
		if err := unix.Lsetxattr(dst, name, value, 0); err != nil {
			return fmt.Errorf("set xattr %s on %s: %w", name, dst, err)
		}
	}
	return nil
}

func listXattrs(path string) ([]string, error) {
	buf, err := readXattrBuffer(func(b []byte) (int, error) { return unix.Llistxattr(path, b) })
	if errors.Is(err, unix.ENOTSUP) {
		return nil, nil // the source filesystem has no xattrs
	}
	if err != nil {
		return nil, fmt.Errorf("list xattrs of %s: %w", path, err)
	}
	var names []string
	for _, name := range bytes.Split(buf, []byte{0}) {
		if len(name) > 0 {
			names = append(names, string(name))
		}
	}
	return names, nil
}

func getXattr(path, name string) ([]byte, error) {
	value, err := readXattrBuffer(func(b []byte) (int, error) { return unix.Lgetxattr(path, name, b) })
	if err != nil {
		return nil, fmt.Errorf("get xattr %s of %s: %w", name, path, err)
	}
	return value, nil
}

// readXattrBuffer calls read with a nil buffer for the size and then with a
// buffer of that size, and retries when the value grew in between.
func readXattrBuffer(read func([]byte) (int, error)) ([]byte, error) {
	for range 4 {
		size, err := read(nil)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			return nil, nil
		}
		buf := make([]byte, size)
		n, err := read(buf)
		if errors.Is(err, unix.ERANGE) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
	return nil, unix.ERANGE
}
