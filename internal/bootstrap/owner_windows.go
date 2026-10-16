package bootstrap

import (
	"errors"
	"os"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

func fileOwner(os.FileInfo) (int, int) { return -1, -1 }

// Windows tasks run as the installing user, so nothing changes owner.
func lchown(string, int, int) error { return nil }

// writerBusy reports whether a process holds the writer lock at path; no
// lock file means no writer.
func writerBusy(path string) (bool, error) {
	if _, e := os.Lstat(path); errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	unlock, e := secure.Lock(path)
	if e != nil {
		return true, nil
	}
	_ = unlock()
	return false, nil
}

func installers() []int { return nil }

// trustedPath is enforced by restrictWindowsDirectory's ACLs instead.
func trustedPath(string, []int, int) error { return nil }
