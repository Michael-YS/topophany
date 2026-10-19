package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

func makeInstallDirectory(path string, perm os.FileMode, _, _ int) error {
	if err := checkDirectoryChain(path); err != nil {
		return err
	}
	if err := secure.MkdirAll(path, perm); err != nil {
		return err
	}
	return checkDirectoryChain(path)
}

func checkDirectoryChain(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil {
			attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || ok && attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
				return errors.New("installation directory chain contains a reparse point or non-directory")
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(p) == p {
			return nil
		}
	}
}
