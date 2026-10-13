package secure

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// Windows replacement can temporarily fail while another writer or a file
// scanner holds a handle. Retry the rename itself; never remove the old file.
func replaceFile(from, to string) error {
	deadline := time.Now().Add(time.Second)
	for {
		err := os.Rename(from, to)
		if err == nil || time.Now().After(deadline) ||
			!(errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33))) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
