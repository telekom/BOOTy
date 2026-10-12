//go:build linux

package provision

import (
	"bufio"
	"context"
	"fmt"
	"errors"
	"io"
	"os/exec"
	"syscall"
	"time"

	"github.com/telekom/BOOTy/pkg/executil"
)

// maxCommandOutputLine bounds one logged output line of a streamed command.
const maxCommandOutputLine = 64 * 1024

// streamWaitDelay bounds how long a finished command may keep its output
// pipes open through background children. Tests shorten it.
var streamWaitDelay = 30 * time.Second

// runStreamingCommand runs argv, inside the chroot root when root is set,
// and calls onLine for every line of its combined stdout and stderr while it
// runs. Tests replace it.
var runStreamingCommand = defaultRunStreamingCommand

func defaultRunStreamingCommand(ctx context.Context, onLine func(string), root string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("run: empty command")
	}
	pr, pw := io.Pipe()
	// executil registers the child, so the PID 1 reaper does not take its
	// exit status. The chroot uses the syscall, so no host chroot binary is
	// needed (the micro initramfs has none), and argv[0] is a path inside
	// root, so skip the host PATH lookup.
	cmd := executil.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Path = argv[0]
	cmd.Err = nil
	// Own process group: cancellation kills the script and its background
	// children, and WaitDelay stops a child that keeps the pipes open from
	// blocking the step after the script exited.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if root != "" {
		cmd.SysProcAttr.Chroot = root
		cmd.Dir = "/"
	}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = streamWaitDelay
	cmd.Stdout = pw
	cmd.Stderr = pw

	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 4096), maxCommandOutputLine)
		for scanner.Scan() {
			onLine(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			onLine(fmt.Sprintf("[output dropped: %v]", err))
		}
		// Keep draining so the command never blocks on a full pipe.
		_, _ = io.Copy(io.Discard, pr)
	}()

	err := cmd.Run()
	_ = pw.Close()
	<-scanDone
	// ErrWaitDelay only means a background child still held the output
	// pipes after the script exited with status 0.
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("run %s: %w", argv[0], err)
	}
	return nil
}
