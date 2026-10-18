//go:build !windows

package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"syscall"
)

func fileOwner(info os.FileInfo) (int, int) {
	if s, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(s.Uid), int(s.Gid)
	}
	return -1, -1
}

// lchown changes who owns path itself, never what a symbolic link there
// points at: a link swapped in by the owner of the directory would
// otherwise hand them a file of root's.
func lchown(path string, uid, gid int) error { return os.Lchown(path, uid, gid) }

// writerBusy reports whether a process holds the writer lock at path. The
// lock is opened as it stands, never through a symbolic link and never
// created, since the data directory belongs to the service and root must
// not be led to touch a file elsewhere. No lock file means no writer.
func writerBusy(path string) (bool, error) {
	f, e := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	if e != nil {
		return false, errors.New("data directory lock is not a regular file")
	}
	defer f.Close()
	if st, e := f.Stat(); e != nil || !st.Mode().IsRegular() {
		return false, errors.New("data directory lock is not a regular file")
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return true, nil
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false, nil
}

// installers are the users an installation running as root may write
// under: root, and the administrator who ran it through sudo.
func installers() []int {
	out := []int{0}
	if id, e := strconv.Atoi(os.Getenv("SUDO_UID")); e == nil && id > 0 {
		out = append(out, id)
	}
	return out
}

// trustedPath refuses a directory that someone other than the trusted
// users could rearrange while root writes into it: one of its existing
// directories, the path itself included, owned by another user or
// writable by anyone beyond its owner (a sticky one owned by root, such as
// /tmp, only shelters what lies below it). Whatever does not exist yet
// root makes itself. Symlinks are rejected before any path is mutated.
// leaf, when not -1, may also own the path itself, as
// the service owns its data directory once installed.
func trustedPath(path string, trusted []int, leaf int) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	chain := []string{}
	for p := abs; ; p = filepath.Dir(p) {
		chain = append(chain, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	// Inspect parents first: even Lstat would resolve an unchecked symlink
	// in a parent before reporting on a deeper component.
	for _, p := range slices.Backward(chain) {
		info, e := os.Lstat(p)
		switch {
		case errors.Is(e, os.ErrNotExist):
		case e != nil:
			return e
		case systemDirectoryAlias(p, info) && p != abs:
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
			if err := trustedPath(resolved, []int{0}, -1); err != nil {
				return err
			}
		case !info.IsDir():
			return fmt.Errorf("%s is not a directory", p)
		default:
			owner, _ := fileOwner(info)
			sticky := info.Mode()&os.ModeSticky != 0 && owner == 0
			allowed := slices.Contains(trusted, owner) || p == abs && owner == leaf && leaf >= 0
			if !allowed || info.Mode().Perm()&0o022 != 0 && !sticky {
				return fmt.Errorf("%s could be changed by another user; choose a directory only root (or you) can write to", p)
			}
		}
	}
	return nil
}

// macOS has fixed root-owned aliases for its system directories.
func systemDirectoryAlias(path string, info os.FileInfo) bool {
	uid, _ := fileOwner(info)
	return runtime.GOOS == "darwin" && uid == 0 && info.Mode()&os.ModeSymlink != 0 && (path == "/var" || path == "/tmp" || path == "/etc")
}
