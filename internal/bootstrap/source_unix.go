//go:build !windows

package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

func trustedBundleSource(_ context.Context, dir string, m Manifest) error {
	if os.Geteuid() != 0 {
		return nil
	}
	trusted := installers()
	names := []string{"manifest.json"}
	for _, f := range m.Files {
		names = append(names, f.Source)
	}
	if m.Role != "client" {
		names = append(names, "service")
	}
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := trustedPath(filepath.Dir(path), trusted, -1); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		owner, _ := fileOwner(info)
		if !info.Mode().IsRegular() || !slices.Contains(trusted, owner) || info.Mode().Perm()&0022 != 0 {
			return errors.New("bundle source must be regular files writable only by the installing administrator")
		}
	}
	return nil
}
