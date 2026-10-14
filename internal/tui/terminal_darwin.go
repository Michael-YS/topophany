package tui

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

type terminal struct {
	in, out *os.File
	old     syscall.Termios
}

func ioctl(fd uintptr, request uintptr, p unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(p))
	if e != 0 {
		return e
	}
	return nil
}
func openTerminal(in, out *os.File) (*terminal, error) {
	t := &terminal{in: in, out: out}
	if e := ioctl(in.Fd(), syscall.TIOCGETA, unsafe.Pointer(&t.old)); e != nil {
		return nil, errors.New("interactive TUI requires a terminal; use --json for non-interactive output")
	}
	var outputMode syscall.Termios
	if e := ioctl(out.Fd(), syscall.TIOCGETA, unsafe.Pointer(&outputMode)); e != nil {
		return nil, errors.New("interactive TUI output must be a terminal; use --json")
	}
	raw := t.old
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Cflag |= syscall.CS8
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Cc[syscall.VMIN] = 0
	raw.Cc[syscall.VTIME] = 0
	if e := ioctl(in.Fd(), syscall.TIOCSETA, unsafe.Pointer(&raw)); e != nil {
		return nil, e
	}
	return t, nil
}
func (t *terminal) Close() error { return ioctl(t.in.Fd(), syscall.TIOCSETA, unsafe.Pointer(&t.old)) }
func (t *terminal) Read() ([]byte, error) {
	b := make([]byte, 256)
	n, e := syscall.Read(int(t.in.Fd()), b)
	if e == syscall.EAGAIN || e == syscall.EINTR {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return b[:n], nil
}
func (t *terminal) Size() (int, int) {
	var s struct{ Rows, Cols, X, Y uint16 }
	if e := ioctl(t.out.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&s)); e != nil || s.Cols == 0 || s.Rows == 0 {
		return 100, 28
	}
	return int(s.Cols), int(s.Rows)
}

// Cell is a cell's height over its width, from the pixel size the terminal
// gives with its rows and columns; 0 when it gives none.
func (t *terminal) Cell() float64 {
	var s struct{ Rows, Cols, X, Y uint16 }
	if e := ioctl(t.out.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&s)); e != nil || s.Cols == 0 || s.Rows == 0 || s.X == 0 || s.Y == 0 {
		return 0
	}
	return float64(s.Y) / float64(s.Rows) / (float64(s.X) / float64(s.Cols))
}
