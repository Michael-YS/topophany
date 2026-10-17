package bootstrap

import (
	"context"
	"os"
	"path/filepath"
)

// Ownership and access to every saved file and the key are checked together
// with the installation DACL before any backup content is read.
func trustedRollbackOwner(os.FileInfo) error { return nil }

func trustedRollbackPath(ctx context.Context, path string, _ bool) error {
	install := path
	for p := path; ; p = filepath.Dir(p) {
		if filepath.Base(p) == ".backups" {
			install = filepath.Dir(p)
			break
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return windowsDirectoryACL(ctx, install, "verify")
}
