//go:build linux

package rescue

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// termFlag is an RFC 4254 section 8 mode that sets one termios flag.
type termFlag struct {
	field func(*unix.Termios) *uint32
	bit   uint32
}

func iflag(t *unix.Termios) *uint32 { return &t.Iflag }
func oflag(t *unix.Termios) *uint32 { return &t.Oflag }
func cflag(t *unix.Termios) *uint32 { return &t.Cflag }
func lflag(t *unix.Termios) *uint32 { return &t.Lflag }

// termChars are the control character opcodes, termFlags the flag opcodes
// that Linux has; dropbear applies the same set.
var (
	termChars = map[byte]int{1: unix.VINTR, 2: unix.VQUIT, 3: unix.VERASE, 4: unix.VKILL, 5: unix.VEOF,
		6: unix.VEOL, 7: unix.VEOL2, 8: unix.VSTART, 9: unix.VSTOP, 10: unix.VSUSP, 12: unix.VREPRINT,
		13: unix.VWERASE, 14: unix.VLNEXT, 18: unix.VDISCARD}
	termFlags = map[byte]termFlag{
		30: {iflag, unix.IGNPAR}, 31: {iflag, unix.PARMRK}, 32: {iflag, unix.INPCK}, 33: {iflag, unix.ISTRIP},
		34: {iflag, unix.INLCR}, 35: {iflag, unix.IGNCR}, 36: {iflag, unix.ICRNL}, 37: {iflag, unix.IUCLC},
		38: {iflag, unix.IXON}, 39: {iflag, unix.IXANY}, 40: {iflag, unix.IXOFF}, 41: {iflag, unix.IMAXBEL},
		42: {iflag, unix.IUTF8},
		50: {lflag, unix.ISIG}, 51: {lflag, unix.ICANON}, 52: {lflag, unix.XCASE}, 53: {lflag, unix.ECHO},
		54: {lflag, unix.ECHOE}, 55: {lflag, unix.ECHOK}, 56: {lflag, unix.ECHONL}, 57: {lflag, unix.NOFLSH},
		58: {lflag, unix.TOSTOP}, 59: {lflag, unix.IEXTEN}, 60: {lflag, unix.ECHOCTL}, 61: {lflag, unix.ECHOKE},
		62: {lflag, unix.PENDIN},
		70: {oflag, unix.OPOST}, 71: {oflag, unix.OLCUC}, 72: {oflag, unix.ONLCR}, 73: {oflag, unix.OCRNL},
		74: {oflag, unix.ONOCR}, 75: {oflag, unix.ONLRET},
		90: {cflag, unix.CS7}, 91: {cflag, unix.CS8}, 92: {cflag, unix.PARENB}, 93: {cflag, unix.PARODD},
	}
)

// setTermModes applies the encoded terminal modes of a pty-req to tty.
// Unknown opcodes (and the speeds, 128 and 129) are ignored, as RFC 4254
// allows.
func setTermModes(tty *os.File, modes []byte) error {
	t, err := unix.IoctlGetTermios(int(tty.Fd()), unix.TCGETS)
	if err != nil {
		return fmt.Errorf("get terminal modes: %w", err)
	}
	for len(modes) > 0 && modes[0] != 0 {
		op := modes[0]
		if op >= 160 || len(modes) < 5 {
			// 160 to 255 have no defined argument: stop like OpenSSH.
			break
		}
		arg := binary.BigEndian.Uint32(modes[1:5])
		modes = modes[5:]
		if c, ok := termChars[op]; ok {
			t.Cc[c] = byte(arg) //nolint:gosec // a control character
		} else if f, ok := termFlags[op]; ok {
			if arg != 0 {
				*f.field(t) |= f.bit
			} else {
				*f.field(t) &^= f.bit
			}
		}
	}
	if err := unix.IoctlSetTermios(int(tty.Fd()), unix.TCSETS, t); err != nil {
		return errors.Join(errors.New("set terminal modes"), err)
	}
	return nil
}
