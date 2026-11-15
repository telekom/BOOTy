//go:build linux

package rescue

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"

	exec "github.com/telekom/BOOTy/pkg/executil"
)

// Like dropbear for root: the shell and home of /etc/passwd in the ramdisk
// and DEFAULT_ROOT_PATH.
const (
	rescueShell = "/bin/sh"
	rescueHome  = "/root"
	rescuePath  = "/usr/sbin:/usr/bin:/sbin:/bin"
)

// sshSignals are the signal names of RFC 4254 that dropbear forwards.
var sshSignals = map[string]syscall.Signal{
	"ABRT": syscall.SIGABRT, "ALRM": syscall.SIGALRM, "FPE": syscall.SIGFPE, "HUP": syscall.SIGHUP,
	"ILL": syscall.SIGILL, "INT": syscall.SIGINT, "KILL": syscall.SIGKILL, "PIPE": syscall.SIGPIPE,
	"QUIT": syscall.SIGQUIT, "SEGV": syscall.SIGSEGV, "TERM": syscall.SIGTERM, "USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2,
}

// session is one SSH session channel: a shell or a command, with or
// without a PTY.
type session struct {
	*rescueConn
	ctx  context.Context
	ch   ssh.Channel
	term string
	pty  *os.File // master side; nil without a pty-req
	tty  string
	cmd  *exec.Cmd
}

func (c *rescueConn) serveSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	// The requests end when the channel or the connection closes (also by
	// the idle timeout); cancel then kills the command's process group.
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	s := &session{rescueConn: c, ctx: ctx, ch: ch}
	defer func() {
		if s.pty != nil {
			_ = s.pty.Close()
		}
	}()
	for req := range reqs {
		ok := false
		switch req.Type {
		case "pty-req":
			ok = s.cmd == nil && s.pty == nil && !c.opts.noPTY && s.openPTY(req.Payload) == nil
		case "window-change":
			ok = s.pty != nil && setWinsize(s.pty, req.Payload) == nil
		case "shell", "exec":
			if s.cmd == nil {
				err := s.start(req)
				if err != nil {
					slog.Warn("rescue SSH session did not start", "error", err)
				}
				ok = err == nil
			}
		case "signal":
			ok = s.signal(req.Payload)
		}
		// Refused: env, subsystem (no SFTP), x11-req, auth-agent-req and the rest.
		if req.WantReply {
			_ = req.Reply(ok, nil)
		}
	}
}

// signal sends a "signal" request to the command, like dropbear (the
// process, not its group).
func (s *session) signal(payload []byte) bool {
	var req struct{ Name string }
	if s.cmd == nil || s.cmd.Process == nil || ssh.Unmarshal(payload, &req) != nil {
		return false
	}
	sig, ok := sshSignals[req.Name]
	return ok && s.cmd.Process.Signal(sig) == nil
}

// openPTY parses a pty-req (term, cols, rows, ...) and opens a pseudo
// terminal of that size.
func (s *session) openPTY(payload []byte) error {
	var req struct {
		Term                 string
		Cols, Rows, PxW, PxH uint32
		Modes                string
	}
	if err := ssh.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("pty-req: %w", err)
	}
	if err := mountDevpts(); err != nil {
		return err
	}
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return fmt.Errorf("open pty: %w", err)
	}
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		_ = m.Close()
		return fmt.Errorf("unlock pty: %w", err)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		_ = m.Close()
		return fmt.Errorf("pty number: %w", err)
	}
	s.pty, s.term, s.tty = m, req.Term, fmt.Sprintf("/dev/pts/%d", n)
	return setWinsize(m, payload[4+len(req.Term):])
}

// mountDevpts mounts devpts at /dev/pts once: the ramdisk mounts none, and
// /dev/ptmx needs it. A devpts that is already there is used.
var mountDevpts = sync.OnceValue(func() error {
	var st unix.Statfs_t
	if unix.Statfs("/dev/pts", &st) == nil && st.Type == unix.DEVPTS_SUPER_MAGIC {
		return nil
	}
	_ = os.MkdirAll("/dev/pts", 0o755)
	if err := unix.Mount("devpts", "/dev/pts", "devpts", unix.MS_NOSUID|unix.MS_NOEXEC, "mode=0620,ptmxmode=0666"); err != nil {
		return fmt.Errorf("mount devpts: %w", err)
	}
	return nil
})

// setWinsize applies a window-change (cols, rows, px width, px height).
func setWinsize(pty *os.File, payload []byte) error {
	if len(payload) < 8 {
		return errors.New("short window-change")
	}
	cols, rows := binary.BigEndian.Uint32(payload), binary.BigEndian.Uint32(payload[4:])
	ws := &unix.Winsize{Row: uint16(rows), Col: uint16(cols)} //nolint:gosec // terminal sizes fit
	if err := unix.IoctlSetWinsize(int(pty.Fd()), unix.TIOCSWINSZ, ws); err != nil {
		return fmt.Errorf("set window size: %w", err)
	}
	return nil
}

// command returns the argv of a shell or exec request: a login shell, or
// "sh -c command"; a command= key option replaces the client's command,
// which goes to SSH_ORIGINAL_COMMAND.
func (s *session) command(req *ssh.Request) (args []string, original string, forced bool, err error) {
	if req.Type == "exec" {
		var cmd struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &cmd); err != nil {
			return nil, "", false, fmt.Errorf("exec request: %w", err)
		}
		original = cmd.Command
	}
	run := original
	if s.opts.hasCommand {
		run, forced = s.opts.command, true
	}
	if req.Type == "shell" && !forced {
		return []string{"-" + filepath.Base(rescueShell)}, "", false, nil
	}
	return []string{rescueShell, "-c", run}, original, forced, nil
}

// env returns the environment that dropbear sets.
func (s *session) env(original string, forced bool) []string {
	env := []string{"USER=" + rescueUser, "LOGNAME=" + rescueUser, "HOME=" + rescueHome, "SHELL=" + rescueShell,
		"PATH=" + rescuePath}
	if s.pty != nil {
		env = append(env, "TERM="+s.term, "SSH_TTY="+s.tty)
	}
	rh, rp, _ := net.SplitHostPort(s.conn.RemoteAddr().String())
	lh, lp, _ := net.SplitHostPort(s.conn.LocalAddr().String())
	env = append(env, "SSH_CONNECTION="+rh+" "+rp+" "+lh+" "+lp, "SSH_CLIENT="+rh+" "+rp+" "+lp)
	if forced {
		env = append(env, "SSH_ORIGINAL_COMMAND="+original)
	}
	return env
}

// start runs the command and closes the channel with its exit status or
// signal.
func (s *session) start(req *ssh.Request) error {
	args, original, forced, err := s.command(req)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(s.ctx, rescueShell, args[1:]...)
	cmd.Args[0] = args[0]
	cmd.Env = s.env(original, forced)
	cmd.Dir = rescueHome
	if unix.Access(rescueHome, unix.X_OK) != nil {
		cmd.Dir = "/"
	}
	var done func()
	if s.pty != nil {
		if done, err = s.attachPTY(cmd); err != nil {
			return err
		}
	} else {
		in, err := cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("stdin: %w", err)
		}
		cmd.Stdout, cmd.Stderr = s.ch, s.ch.Stderr()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		go func() { _, _ = io.Copy(in, s.ch); _ = in.Close() }()
		done = func() {}
	}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd
	go func() {
		_ = cmd.Wait()
		done()
		s.sendExit(cmd.ProcessState)
		_ = s.ch.Close()
	}()
	return nil
}

// sendExit sends exit-signal for a command killed by a signal, else
// exit-status.
func (s *session) sendExit(ps *os.ProcessState) {
	if ps == nil {
		_, _ = s.ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{255}))
		return
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		for name, sig := range sshSignals {
			if sig == ws.Signal() {
				_, _ = s.ch.SendRequest("exit-signal", false, ssh.Marshal(struct {
					Signal     string
					CoreDumped bool
					Msg, Lang  string
				}{name, ws.CoreDump(), "", ""}))
				return
			}
		}
	}
	_, _ = s.ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(ps.ExitCode() & 0xff)})) //nolint:gosec // masked
}

// attachPTY gives cmd the terminal as its controlling terminal and copies
// between the terminal and the channel. The returned func waits for the
// output after the command ended.
func (s *session) attachPTY(cmd *exec.Cmd) (func(), error) {
	tty, err := os.OpenFile(s.tty, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open tty: %w", err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	out := make(chan struct{})
	go func() { _, _ = io.Copy(s.pty, s.ch) }()
	go func() { _, _ = io.Copy(s.ch, s.pty); close(out) }()
	return func() {
		// The terminal reads EIO once the last holder of the tty side closed it.
		_ = tty.Close()
		select {
		case <-out:
		case <-time.After(2 * time.Second):
		}
	}, nil
}
