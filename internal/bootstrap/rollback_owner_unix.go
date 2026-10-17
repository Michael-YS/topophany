//go:build !windows

package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

func trustedRollbackOwner(info os.FileInfo) error {
	if uid, _ := fileOwner(info); uid != os.Geteuid() {
		return errors.New("rollback files must belong to the installing user (root for privileged installations)")
	}
	return nil
}

// Unlike bundle input, privileged backups cannot be supplied by SUDO_UID or
// service identities. Even a private file is untrusted if its owner can edit it.
func trustedRollbackPath(ctx context.Context, path string, private bool) error {
	p, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	leaf := p
	for {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		uid, _ := fileOwner(info)
		// macOS exposes its root-controlled temporary and system trees
		// through these fixed aliases. No installation/backup link is allowed.
		if runtime.GOOS == "darwin" && p != leaf && uid == 0 && info.Mode()&os.ModeSymlink != 0 && (p == "/var" || p == "/tmp" || p == "/etc") {
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
			return trustedPath(resolved, []int{0}, -1)
		}
		sticky := info.Mode()&os.ModeSticky != 0 && uid == 0
		if !info.IsDir() || uid != 0 && uid != os.Geteuid() || info.Mode().Perm()&0022 != 0 && !sticky {
			return errors.New("rollback directory chain must be owned and writable only by the installing user or root, without symlinks")
		}
		if p == leaf || filepath.Base(p) == ".backups" {
			if err := trustedRollbackOwner(info); err != nil {
				return err
			}
			if (private || filepath.Base(p) == ".backups") && info.Mode().Perm()&0077 != 0 {
				return errors.New("rollback backup directories must be private")
			}
		}
		parent := filepath.Dir(p)
		if parent == p {
			return nil
		}
		p = parent
	}
}
