package bootstrap

import (
	"os"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

func makeInstallDirectory(path string, perm os.FileMode, _, _ int) error {
	return secure.MkdirAll(path, perm)
}
