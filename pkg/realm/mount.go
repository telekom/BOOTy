//go:build linux

package realm

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"syscall"
)

// isMounted checks whether a path is already a mount point by reading /proc/mounts.
func isMounted(path string) bool {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck // best-effort close

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[1] == path {
			return true
		}
	}
	return false
}

// DefaultMounts will return the default mounts.
func DefaultMounts() *Mounts {
	return &Mounts{
		Mount: []Mount{
			{Name: "bin", Path: "/bin", Mode: 0o777},
			{Name: "dev", Source: "devtmpfs", Path: "/dev", FSType: "devtmpfs", Flags: syscall.MS_MGC_VAL, Mode: 0o777},
			{Name: "etc", Path: "/etc", Mode: 0o777},
			{Name: "home", Path: "/home", Mode: 0o777},
			{Name: "mnt", Path: "/mnt", Mode: 0o777},
			{Name: "proc", Source: "proc", Path: "/proc", FSType: "proc", Mode: 0o777},
			{Name: "sys", Source: "sysfs", Path: "/sys", FSType: "sysfs", Mode: 0o777},
			{Name: "tmp", Source: "tmpfs", Path: "/tmp", FSType: "tmpfs", Mode: 0o777},
			{Name: "usr", Path: "/usr", Mode: 0o777},
		},
	}
}

// CreateFolder creates directories for all mounts that have CreateMount set.
func (m *Mounts) CreateFolder() error {

	for x := range m.Mount {
		if m.Mount[x].CreateMount {
			err := os.MkdirAll(m.Mount[x].Path, m.Mount[x].Mode)
			if err != nil {
				return fmt.Errorf("folder [%s] create error: %w", m.Mount[x].Path, err)
			}
			slog.Info("Folder created", "name", m.Mount[x].Name, "path", m.Mount[x].Path)
		}
	}
	return nil
}

// MountAll mounts all enabled partitions, skipping those already mounted.
func (m *Mounts) MountAll() error {
	for x := range m.Mount {
		if !m.Mount[x].EnableMount {
			continue
		}
		if isMounted(m.Mount[x].Path) {
			slog.Info("Already mounted, skipping", "name", m.Mount[x].Name, "path", m.Mount[x].Path)
			continue
		}
		err := syscall.Mount(m.Mount[x].Source, m.Mount[x].Path, m.Mount[x].FSType, m.Mount[x].Flags, m.Mount[x].Options)
		if err != nil {
			return fmt.Errorf("mounting [%s] -> [%s]: %w", m.Mount[x].Source, m.Mount[x].Path, err)
		}
		slog.Info("Mounted", "name", m.Mount[x].Name, "path", m.Mount[x].Path)
	}
	return nil
}

// MountNamed mounts a single named partition, skipping if already mounted.
func (m *Mounts) MountNamed(name string, remove bool) error {
	for x := range m.Mount {
		if m.Mount[x].Name != name || !m.Mount[x].EnableMount {
			continue
		}

		if isMounted(m.Mount[x].Path) {
			slog.Info("Already mounted, skipping", "name", m.Mount[x].Name, "path", m.Mount[x].Path)
		} else {
			err := syscall.Mount(m.Mount[x].Source, m.Mount[x].Path, m.Mount[x].FSType, m.Mount[x].Flags, m.Mount[x].Options)
			if err != nil {
				return fmt.Errorf("mounting [%s] -> [%s]: %w", m.Mount[x].Source, m.Mount[x].Path, err)
			}
			slog.Info("Mounted", "name", m.Mount[x].Name, "path", m.Mount[x].Path)
		}

		if remove {
			m.Mount = append(m.Mount[:x], m.Mount[x+1:]...)
		}
		return nil
	}
	return nil
}

// UnMountAll will unmount all partitions.
func (m *Mounts) UnMountAll() error {

	for x := range m.Mount {
		err := syscall.Unmount(m.Mount[x].Path, int(m.Mount[x].Flags)) //nolint:gosec // G115: flags are small values, no overflow risk

		if err != nil {
			return fmt.Errorf("unmounting [%s] -> [%s]: %w", m.Mount[x].Source, m.Mount[x].Path, err)
		}
		slog.Info("Unmounted", "name", m.Mount[x].Name, "path", m.Mount[x].Path)
	}

	return nil
}

// UnMountNamed will unmount a named partition.
func (m *Mounts) UnMountNamed(name string) error {

	for x := range m.Mount {
		if m.Mount[x].Name != name {
			continue
		}

		err := syscall.Unmount(m.Mount[x].Path, syscall.MNT_FORCE)
		if err != nil {
			return fmt.Errorf("unmounting [%s] -> [%s]: %w", m.Mount[x].Source, m.Mount[x].Path, err)
		}

		slog.Info("Unmounted", "name", m.Mount[x].Name, "path", m.Mount[x].Path)
		// Remove this element
		m.Mount = append(m.Mount[:x], m.Mount[x+1:]...)
		return nil
	}
	return fmt.Errorf("unable to find mount [%s]", name)
}

// GetMount returns a pointer to the named mount.
func (m *Mounts) GetMount(name string) *Mount {

	for x := range m.Mount {
		if m.Mount[x].Name == name {
			return &m.Mount[x]
		}
	}
	return nil
}
