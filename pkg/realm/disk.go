//go:build linux

package realm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Update partitions
// partprobe /dev/sda

// Enable volumes
// lvm vgchange -ay

// mount chroot
// mkdir /mnt
// mount /dev/ubuntu-vg/root /mnt

// PROC mount
// mount -t proc none /mnt/proc

// DEV mount
// mount -o bind /dev /mnt/dev

// Grow partition
// chroot /mnt /usr/bin/growpart /dev/sda 1
// chroot /mnt /sbin/pvresize /dev/sda1
// chroot /mnt /sbin/lvresize -l +100%FREE /dev/ubuntu-vg/root
// chroot /mnt /sbin/resize2fs   /dev/ubuntu-vg/root

// PartProbe will update partitions - will enable any volumes.
func PartProbe(device string) error {
	// TTY hack to support ctrl+c
	cmd := exec.CommandContext(context.Background(), "partprobe", device)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("partition probe command error: %w", err)
	}
	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("partition probe error: %w", err)
	}
	// Ensure that disks are mounted and we're in a position to mount them
	time.Sleep(time.Second * 2)
	return nil
}

// EnableLVM - will enable any volumes.
func EnableLVM() error {
	// TTY hack to support ctrl+c
	cmd := exec.CommandContext(context.Background(), "/sbin/lvm", "vgchange", "-ay")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("linux volume command error: %w", err)
	}
	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("linux volume error: %w", err)
	}
	return nil
}

// MountRootVolume - will create a mountpoint and mount the root volume.
func MountRootVolume(rootVolume string) (*Mounts, error) {
	m := Mounts{}
	root := Mount{
		CreateMount: true,
		EnableMount: true,
		Name:        "root",
		Source:      rootVolume,
		Path:        "/mnt",
		FSType:      "ext4",
	}
	m.Mount = append(m.Mount, root)

	dev := Mount{
		CreateMount: false,
		EnableMount: true,
		Name:        "dev",
		Source:      "devtmpfs",
		Path:        "/mnt/dev",
		FSType:      "devtmpfs",
		Flags:       syscall.MS_MGC_VAL,
		Mode:        0o777,
	}
	m.Mount = append(m.Mount, dev)

	proc := Mount{
		CreateMount: false,
		EnableMount: true,
		Name:        "proc",
		Source:      "proc",
		Path:        "/mnt/proc",
		FSType:      "proc",
		Mode:        0o777,
	}
	m.Mount = append(m.Mount, proc)

	err := m.CreateFolder()
	if err != nil {
		return nil, err
	}

	err = m.MountAll()
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GrowLVMRoot will grow the root filesystem.
func GrowLVMRoot(drive, volume string, partition int) error {
	// chroot /mnt /usr/bin/growpart /dev/sda 1
	// chroot /mnt /sbin/pvresize /dev/sda1
	// chroot /mnt /sbin/lvresize -l +100%FREE /dev/ubuntu-vg/root
	// chroot /mnt /sbin/resize2fs   /dev/ubuntu-vg/root
	chrootCommands := make([][]string, 0, 4)

	growpartition := []string{"/mnt", "/usr/bin/growpart", drive, fmt.Sprintf("%d", partition)}
	chrootCommands = append(chrootCommands, growpartition)

	resizePhysicalVolume := []string{"/mnt", "/sbin/pvresize", fmt.Sprintf("%s%d", drive, partition)}
	chrootCommands = append(chrootCommands, resizePhysicalVolume)

	resizeLogicalVolume := []string{"/mnt", "/sbin/lvresize", "-l", "+100%FREE", volume}
	chrootCommands = append(chrootCommands, resizeLogicalVolume)

	resizeFilesystem := []string{"/mnt", "/sbin/resize2fs", volume}
	chrootCommands = append(chrootCommands, resizeFilesystem)
	for x := range chrootCommands {
		cmd := exec.CommandContext(context.Background(), "/usr/sbin/chroot", chrootCommands[x]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

		err := cmd.Start()
		if err != nil {
			return fmt.Errorf("chroot command error: %w", err)
		}
		err = cmd.Wait()
		if err != nil {
			return fmt.Errorf("chroot error: %w", err)
		}
	}
	return nil
}

// Wipe will clean the beginning of the disk.
func Wipe(device string) error {
	// wipe
	// dd if=/dev/zero of=/dev/sda bs=1024k count=100
	slog.Info("Wiping disk")
	input := "if=/dev/zero"
	output := fmt.Sprintf("of=%s", device)
	blockSize := "bs=1024k"
	count := "count=100"

	cmd := exec.CommandContext(context.Background(), "/bin/dd", input, output, blockSize, count)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("disk wipe command error: %w", err)
	}
	slog.Info("Waiting for command to finish...")
	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("disk wipe: %w", err)
	}
	return nil
}
