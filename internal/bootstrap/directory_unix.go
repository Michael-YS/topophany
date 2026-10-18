//go:build !windows

package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

func makeInstallDirectory(path string, perm os.FileMode, uid, gid int) error {
	if os.Geteuid() != 0 {
		return secure.MkdirAll(path, perm)
	}
	return makeTrustedDirectory(path, perm, installers(), uid, gid)
}

// makeTrustedDirectory creates and changes ownership through verified directory
// handles. Missing entries under a sticky directory cannot redirect a later
// MkdirAll or chown. On failure, only our own still-empty directories are removed.
func makeTrustedDirectory(path string, perm os.FileMode, trusted []int, uid, gid int) (err error) {
	if err = trustedPath(path, trusted, uid); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return err
	}
	roots := []*os.Root{root}
	type created struct {
		parent *os.Root
		name   string
		info   os.FileInfo
	}
	made := []created{}
	defer func() {
		if err != nil {
			for _, c := range slices.Backward(made) {
				if now, e := c.parent.Lstat(c.name); e == nil && os.SameFile(c.info, now) {
					_ = c.parent.Remove(c.name) // never recursively remove data
				}
			}
		}
		for _, r := range roots {
			_ = r.Close()
		}
	}()
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	for i, name := range parts {
		info, e := root.Lstat(name)
		if errors.Is(e, os.ErrNotExist) {
			if e = root.Mkdir(name, perm); e != nil {
				return e
			}
			info, e = root.Lstat(name)
			if e != nil {
				return e
			}
			made = append(made, created{root, name, info})
			parent, e := root.Open(".")
			if e != nil {
				return e
			}
			e = parent.Sync()
			_ = parent.Close()
			if e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		var next *os.Root
		prefix := "/" + filepath.Join(parts[:i+1]...)
		if systemDirectoryAlias(prefix, info) && i < len(parts)-1 {
			resolved, resolveErr := filepath.EvalSymlinks(prefix)
			if resolveErr != nil {
				return resolveErr
			}
			if e = trustedPath(resolved, []int{0}, -1); e != nil {
				return e
			}
			next, e = os.OpenRoot(resolved)
		} else {
			owner, _ := fileOwner(info)
			sticky := owner == 0 && info.Mode()&os.ModeSticky != 0
			allowed := slices.Contains(trusted, owner) || i == len(parts)-1 && uid >= 0 && owner == uid
			if !info.IsDir() || !allowed || info.Mode().Perm()&0022 != 0 && !sticky {
				return errors.New("installation directory chain changed or contains an untrusted component")
			}
			next, e = root.OpenRoot(name)
			if e == nil {
				opened, statErr := next.Stat(".")
				if statErr != nil || !os.SameFile(info, opened) {
					_ = next.Close()
					return errors.New("installation directory changed while opening it")
				}
			}
		}
		if e != nil {
			return e
		}
		roots = append(roots, next)
		root = next
	}
	if uid >= 0 {
		f, e := root.OpenFile(".", os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			return e
		}
		err = f.Chown(uid, gid)
		_ = f.Close()
	}
	return err
}
