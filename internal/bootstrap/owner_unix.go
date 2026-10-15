//go:build !windows

package bootstrap

import (
	"os"
	"syscall"
)

func fileOwner(info os.FileInfo) (int, int) {
	if s, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(s.Uid), int(s.Gid)
	}
	return -1, -1
}
