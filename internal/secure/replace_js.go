//go:build js && wasm

package secure

import "errors"

// The browser preview has no writable filesystem. Fail closed if a viewer
// preference dialog attempts to persist settings.
func replaceFile(_, _ string) error {
	return errors.New("file writes are unavailable in the browser preview")
}
