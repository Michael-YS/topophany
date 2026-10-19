// Package memlimit configures Go's soft memory target and an OS-enforced cap.
package memlimit

import (
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"strconv"
	"strings"
)

// Parse accepts bytes or an integer followed by KiB, MiB or GiB; 0 disables it.
func Parse(raw string) (int64, error) {
	scale := int64(1)
	n := raw
	for _, u := range []struct {
		s string
		n int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(raw, u.s) {
			scale = u.n
			n = strings.TrimSuffix(raw, u.s)
			break
		}
	}
	v, e := strconv.ParseUint(n, 10, 63)
	if e != nil || v > uint64(math.MaxInt64/scale) {
		return 0, errors.New("memory size must be nonnegative bytes or an integer with KiB, MiB or GiB")
	}
	return int64(v) * scale, nil
}

// Apply runs before opening collector state or the worker outbox. Zero soft
// leaves GOMEMLIMIT in force; a hard cap automatically reserves 25% for non-Go
// memory and filesystem cache unless an even smaller soft target was requested.
func Apply(soft, hard int64) (func(), error) {
	if soft < 0 || hard < 0 || soft > 0 && soft < 32<<20 || hard > 0 && hard < 64<<20 {
		return nil, errors.New("memory-limit must be 0 or at least 32MiB; memory-max must be 0 or at least 64MiB")
	}
	old := debug.SetMemoryLimit(-1)
	if hard > 0 {
		if soft > hard-hard/4 {
			return nil, errors.New("memory-limit must be at most 75% of memory-max (reserve space for non-Go memory)")
		}
		target := hard - hard/4
		if soft == 0 {
			soft = min(old, target)
		}
	}
	cleanup := func() {}
	if hard > 0 {
		var err error
		cleanup, err = hardLimit(hard)
		if err != nil {
			return nil, fmt.Errorf("cannot enforce --memory-max: %w", err)
		}
	}
	if soft > 0 {
		debug.SetMemoryLimit(soft)
	}
	return func() { debug.SetMemoryLimit(old); cleanup() }, nil
}
