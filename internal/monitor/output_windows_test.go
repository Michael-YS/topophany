//go:build windows

package monitor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWindowsReaderWithoutDeleteSharing(t *testing.T) {
	c := testConfig()
	c.Revision = "fixture"
	c.Output = filepath.Join(t.TempDir(), "metrics.json")
	out, e := OpenOutput(c.Output, c.Name)
	if e != nil {
		t.Fatal(e)
	}
	defer out.Close()
	now := time.Now().UTC()
	p := Build(c, hostInput(now), now)
	if e = out.Write(p, time.Second, true); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(c.Output)
	path, e := syscall.UTF16PtrFromString(c.Output)
	if e != nil {
		t.Fatal(e)
	}
	handle, e := syscall.CreateFile(path, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		t.Fatal(e)
	}
	p.GeneratedAt = now.Add(time.Minute)
	e = out.Write(p, time.Second, true)
	syscall.CloseHandle(handle)
	if e == nil {
		t.Fatal("replacement unexpectedly ignored reader sharing mode")
	}
	after, _ := os.ReadFile(c.Output)
	if string(before) != string(after) {
		t.Fatal("failed replacement damaged previous snapshot")
	}
	if e = out.Write(p, time.Second, true); e != nil {
		t.Fatal("replacement did not recover", e)
	}
}
