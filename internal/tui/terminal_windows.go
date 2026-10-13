package tui

import (
	"errors"
	"os"
	"strconv"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")
var getMode = kernel.NewProc("GetConsoleMode")
var setMode = kernel.NewProc("SetConsoleMode")
var peekInput = kernel.NewProc("PeekConsoleInputW")
var readInput = kernel.NewProc("ReadConsoleInputW")
var getScreen = kernel.NewProc("GetConsoleScreenBufferInfo")

type terminal struct {
	in, out       *os.File
	oldIn, oldOut uint32
}

func openTerminal(in, out *os.File) (*terminal, error) {
	t := &terminal{in: in, out: out}
	a, _, _ := getMode.Call(in.Fd(), uintptr(unsafe.Pointer(&t.oldIn)))
	b, _, _ := getMode.Call(out.Fd(), uintptr(unsafe.Pointer(&t.oldOut)))
	if a == 0 || b == 0 {
		return nil, errors.New("interactive TUI requires a Windows console; use --json for redirected output")
	}
	// Raw keys, no quick-edit selection, window + mouse input records.
	mode := (t.oldIn &^ uint32(1|2|4|64)) | 128 | 16 | 8
	a, _, _ = setMode.Call(in.Fd(), uintptr(mode))
	b, _, _ = setMode.Call(out.Fd(), uintptr(t.oldOut|4))
	if a == 0 || b == 0 {
		t.Close()
		return nil, errors.New("cannot enable console input/ANSI mode")
	}
	return t, nil
}
func (t *terminal) Close() error {
	_, _, _ = setMode.Call(t.in.Fd(), uintptr(t.oldIn))
	_, _, _ = setMode.Call(t.out.Fd(), uintptr(t.oldOut))
	return nil
}

type inputRecord struct {
	Type, Pad                uint16
	Down                     int32
	Repeat, VKey, Scan, Char uint16
	Control                  uint32
}

func (t *terminal) Read() ([]byte, error) {
	var r inputRecord
	var count uint32
	n, _, _ := peekInput.Call(t.in.Fd(), uintptr(unsafe.Pointer(&r)), 1, uintptr(unsafe.Pointer(&count)))
	if n == 0 {
		return nil, errors.New("console input unavailable")
	}
	if count == 0 {
		return nil, nil
	}
	n, _, _ = readInput.Call(t.in.Fd(), uintptr(unsafe.Pointer(&r)), 1, uintptr(unsafe.Pointer(&count)))
	if n == 0 {
		return nil, errors.New("console read failed")
	}
	if r.Type == 2 {
		return t.mouse(r), nil
	}
	if r.Type != 1 || r.Down == 0 {
		return nil, nil
	}
	if r.Char != 0 {
		b := make([]byte, 4)
		n := utf8.EncodeRune(b, rune(r.Char))
		return b[:n], nil
	}
	switch r.VKey {
	case 0x26:
		return []byte("\x1b[A"), nil
	case 0x28:
		return []byte("\x1b[B"), nil
	case 0x27:
		return []byte("\x1b[C"), nil
	case 0x25:
		return []byte("\x1b[D"), nil
	case 0x21:
		return []byte("\x1b[5~"), nil
	case 0x22:
		return []byte("\x1b[6~"), nil
	case 0x24:
		return []byte("\x1b[H"), nil
	case 0x23:
		return []byte("\x1b[F"), nil
	case 0x2E:
		return []byte("\x1b[3~"), nil
	}
	return nil, nil
}

// mouse re-encodes a MOUSE_EVENT record (same union bytes as the key layout
// above) as an SGR report, so one decoder handles every platform.
func (t *terminal) mouse(r inputRecord) []byte {
	x, y := int(int16(uint32(r.Down))), int(int16(uint32(r.Down)>>16))
	buttons := uint32(r.Repeat) | uint32(r.VKey)<<16
	flags := r.Control
	var s struct {
		SizeX, SizeY, CursorX, CursorY       int16
		Attributes                           uint16
		Left, Top, Right, Bottom, MaxX, MaxY int16
	}
	if n, _, _ := getScreen.Call(t.out.Fd(), uintptr(unsafe.Pointer(&s))); n != 0 {
		x, y = x-int(s.Left), y-int(s.Top)
	}
	b := -1
	switch {
	case flags == 4:
		b = 65
		if int16(buttons>>16) > 0 {
			b = 64
		}
	case flags == 0 && buttons&1 != 0:
		b = 0
	}
	if b < 0 || x < 0 || y < 0 {
		return nil
	}
	return []byte("\x1b[<" + strconv.Itoa(b) + ";" + strconv.Itoa(x+1) + ";" + strconv.Itoa(y+1) + "M")
}
func (t *terminal) Size() (int, int) {
	var s struct {
		SizeX, SizeY, CursorX, CursorY       int16
		Attributes                           uint16
		Left, Top, Right, Bottom, MaxX, MaxY int16
	}
	n, _, _ := getScreen.Call(t.out.Fd(), uintptr(unsafe.Pointer(&s)))
	if n == 0 {
		return 100, 28
	}
	return int(s.Right - s.Left + 1), int(s.Bottom - s.Top + 1)
}
