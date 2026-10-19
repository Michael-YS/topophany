//go:build !linux

package memlimit

import (
	"fmt"
	"runtime"
)

func hardLimit(int64) (func(), error) {
	return nil, fmt.Errorf("OS hard memory limits are not supported on %s; --memory-limit is a Go soft target only; use a Linux cgroup/container for a kernel-enforced cap", runtime.GOOS)
}
