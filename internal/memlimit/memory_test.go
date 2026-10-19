package memlimit

import (
	"runtime"
	"runtime/debug"
	"testing"
)

func TestParseAndValidation(t *testing.T) {
	for s, want := range map[string]int64{"0": 0, "128MiB": 128 << 20, "1GiB": 1 << 30, "65536": 65536, "32KiB": 32768, "64B": 64} {
		got, e := Parse(s)
		if e != nil || got != want {
			t.Fatalf("%q: %d %v", s, got, e)
		}
	}
	for _, s := range []string{"", "-1", "1.5MiB", "32MB", "9223372036854775808", "9223372036854775807GiB"} {
		if _, e := Parse(s); e == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	old := debug.SetMemoryLimit(-1)
	restore, e := Apply(64<<20, 0)
	if e != nil {
		t.Fatal(e)
	}
	if debug.SetMemoryLimit(-1) != 64<<20 {
		t.Fatal("soft target not applied")
	}
	restore()
	if debug.SetMemoryLimit(-1) != old {
		t.Fatal("soft target not restored")
	}
	for _, pair := range [][2]int64{{-1, 0}, {1, 0}, {0, 1}, {128 << 20, 128 << 20}} {
		if stop, e := Apply(pair[0], pair[1]); e == nil {
			stop()
			t.Fatal("invalid limits accepted", pair)
		}
	}
}

func TestUnsupportedHardCapFailsClosed(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux has a kernel implementation")
	}
	old := debug.SetMemoryLimit(-1)
	if stop, e := Apply(64<<20, 128<<20); e == nil {
		stop()
		t.Fatal("unsupported hard cap silently accepted")
	}
	if debug.SetMemoryLimit(-1) != old {
		t.Fatal("failed hard cap changed runtime policy")
	}
}
