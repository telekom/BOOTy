//go:build linux

package provision

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/telekom/BOOTy/pkg/executil"
)

// maxCommandOutputLine bounds one logged output line of a streamed command.
const maxCommandOutputLine = 64 * 1024

// runStreamingCommand runs a host command and calls onLine for every line of
// its combined stdout and stderr while it runs. Tests replace it.
var runStreamingCommand = defaultRunStreamingCommand

func defaultRunStreamingCommand(ctx context.Context, onLine func(string), name string, args ...string) error {
	pr, pw := io.Pipe()
	// executil registers the child, so the PID 1 reaper does not take its
	// exit status.
	cmd := executil.CommandContext(ctx, name, args...)
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
		if err := scanner.Err(); errors.Is(err, bufio.ErrTooLong) {
			onLine("[output line too long, rest of the output dropped]")
		}
		// Keep draining so the command never blocks on a full pipe.
		_, _ = io.Copy(io.Discard, pr)
	}()

	err := cmd.Run()
	_ = pw.Close()
	<-scanDone
	if err != nil {
		return fmt.Errorf("run %s: %w", name, err)
	}
	return nil
}
