package memlimit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Configure the current cgroup without migrating the process: cgroup v2 does
// not migrate existing memory charges when a running process is moved. This
// way allocations made before main, recovery, and subprocesses are all covered.
// An existing systemd/container cap may be used without write permission.
func hardLimit(limit int64) (func(), error) {
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil, err
	}
	rel, err := unifiedPath(string(raw))
	if err != nil {
		return nil, err
	}
	if err = configureCgroup("/sys/fs/cgroup", rel, limit, os.Getpid()); err != nil {
		return nil, fmt.Errorf("%w; start in a private writable cgroup, or set systemd MemoryMax=/MemorySwapMax=0 or container --memory/--memory-swap to the same cap", err)
	}
	return func() {}, nil
}

func configureCgroup(root, rel string, limit int64, pid int) error {
	dir := filepath.Join(root, rel)
	// An ancestor's cap also contains this process and all its children. Never
	// raise a stricter existing cap, including a container's inherited cap.
	memory, swap := int64(0), int64(-1)
	for path := dir; ; path = filepath.Dir(path) {
		n, err := readControlLimit(filepath.Join(path, "memory.max"))
		if err == nil && n > 0 && (memory == 0 || n < memory) {
			memory = n
		}
		n, err = readControlLimit(filepath.Join(path, "memory.swap.max"))
		if err == nil && n >= 0 && (swap < 0 || n < swap) {
			swap = n
		}
		if path == root {
			break
		}
	}
	if memory > 0 && memory <= limit && swap == 0 {
		return nil
	}
	if dir == root {
		return errors.New("refusing to modify the root cgroup")
	}
	// Lowering resource limits in a shared cgroup could kill unrelated work.
	procs, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
	if err != nil {
		return err
	}
	for _, p := range strings.Fields(string(procs)) {
		if p != strconv.Itoa(pid) {
			return errors.New("refusing to change a shared cgroup")
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return errors.New("refusing to change a cgroup with child groups")
		}
	}
	if err = writeControl(filepath.Join(dir, "memory.swap.max"), "0"); err != nil {
		return err
	}
	current, err := readControlLimit(filepath.Join(dir, "memory.max"))
	if err != nil {
		return err
	}
	if current < 0 || current > limit {
		if err = writeControl(filepath.Join(dir, "memory.max"), strconv.FormatInt(limit, 10)); err != nil {
			return err
		}
	}
	// Read back kernel state: never silently degrade to a soft target.
	current, err = readControlLimit(filepath.Join(dir, "memory.max"))
	if err != nil || current < 0 || current > limit {
		return errors.New("kernel memory cap could not be verified")
	}
	current, err = readControlLimit(filepath.Join(dir, "memory.swap.max"))
	if err != nil || current != 0 {
		return errors.New("kernel swap cap could not be verified")
	}
	return nil
}

func unifiedPath(raw string) (string, error) {
	for line := range strings.SplitSeq(strings.TrimSpace(raw), "\n") {
		if rel, ok := strings.CutPrefix(line, "0::"); ok {
			if !strings.HasPrefix(rel, "/") {
				break
			}
			for part := range strings.SplitSeq(rel, "/") {
				if part == ".." {
					return "", errors.New("cgroup namespace path escapes its mount")
				}
			}
			return filepath.Clean(rel), nil
		}
	}
	return "", errors.New("cgroup v2 memory controller required; cgroup v1 is unsupported")
}

func readControlLimit(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	if s == "max" {
		return -1, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

func writeControl(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString(value)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
