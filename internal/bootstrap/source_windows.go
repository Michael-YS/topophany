package bootstrap

import "context"

func trustedBundleSource(ctx context.Context, dir string, _ Manifest) error {
	return windowsDirectoryACL(ctx, dir, "verify")
}
