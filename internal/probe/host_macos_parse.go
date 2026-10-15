package probe

import (
	"context"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Kept portable so macOS output fixtures can be validated on every build host.
func sampleDarwinRuntime(ctx context.Context, names []string, command Command) (hostRuntime, error) {
	s := newHostRuntime(names)
	top, err := command(ctx, "/usr/bin/top", "-l", "2", "-n", "0", "-s", "1", "-R", "-F")
	if err != nil {
		return s, hostReadError("cpu")
	}
	s.CPUPercent, err = parseDarwinCPU(string(top))
	if err != nil {
		return s, err
	}
	s.CPUSampleMS = 1000
	mem, err := command(ctx, "/usr/sbin/sysctl", "-n", "hw.memsize")
	if err != nil {
		return s, hostReadError("memory")
	}
	s.MemoryTotal, err = counter(strings.TrimSpace(string(mem)))
	if err != nil || s.MemoryTotal == 0 {
		return s, local("host", "invalid_memory_counters")
	}
	vm, err := command(ctx, "/usr/bin/vm_stat")
	if err != nil {
		return s, hostReadError("memory")
	}
	s.MemoryAvailable, err = parseDarwinMemory(string(vm), s.MemoryTotal)
	if err != nil {
		return s, err
	}
	s.MemoryBasis = "macos_estimated_free_inactive_speculative"
	ps, err := command(ctx, "/bin/ps", "-ww", "-axo", "pid=,stat=,rss=,comm=")
	if err != nil {
		return s, hostReadError("processes")
	}
	err = parseDarwinProcesses(string(ps), &s)
	return s, err
}

var darwinCPU = regexp.MustCompile(`(?m)^CPU usage:.*?([0-9]+(?:\.[0-9]+)?)% idle\s*$`)

func parseDarwinCPU(raw string) (float64, error) {
	matches := darwinCPU.FindAllStringSubmatch(raw, -1)
	// The first top sample is since boot. Only the second is the sampled interval.
	if len(matches) != 2 {
		return 0, local("host", "invalid_cpu_counters")
	}
	idle, err := strconv.ParseFloat(matches[1][1], 64)
	if err != nil || idle < 0 || idle > 100 {
		return 0, local("host", "invalid_cpu_counters")
	}
	return 100 - idle, nil
}

var darwinPageSize = regexp.MustCompile(`page size of ([0-9]+) bytes`)

func parseDarwinMemory(raw string, total uint64) (uint64, error) {
	found := darwinPageSize.FindStringSubmatch(raw)
	if len(found) != 2 {
		return 0, local("host", "invalid_memory_counters")
	}
	page, err := counter(found[1])
	if err != nil || page == 0 {
		return 0, local("host", "invalid_memory_counters")
	}
	want := map[string]bool{"Pages free": true, "Pages inactive": true, "Pages speculative": true}
	var pages uint64
	for _, line := range strings.Split(raw, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok || !want[key] {
			continue
		}
		n, err := counter(strings.TrimSuffix(strings.TrimSpace(val), "."))
		if err != nil || n > math.MaxUint64-pages {
			return 0, local("host", "invalid_memory_counters")
		}
		pages += n
		delete(want, key)
	}
	available, err := bytesFromUnits(pages, page)
	if err != nil || len(want) != 0 || available > total {
		return 0, local("host", "invalid_memory_counters")
	}
	return available, nil
}

func parseDarwinProcesses(raw string, s *hostRuntime) error {
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// The executable path may contain spaces. Consume only the three
		// leading numeric/state fields, keeping the whole command as the last.
		var f [3]string
		rest := strings.TrimSpace(line)
		for i := 0; i < 3; i++ {
			j := strings.IndexAny(rest, " \t")
			if j < 0 {
				return local("host", "invalid_process_counters")
			}
			f[i] = rest[:j]
			rest = strings.TrimSpace(rest[j:])
		}
		pid, e1 := counter(f[0])
		rss, e2 := counter(f[2])
		if e1 != nil || e2 != nil || pid > math.MaxInt32 || rest == "" || s.Processes >= 65536 {
			return local("host", "invalid_process_counters")
		}
		// ps falls back to (p_comm) when another user's argv is unreadable.
		// Normalize only that form; parentheses in an executable path are literal.
		name := rest
		if strings.HasPrefix(name, "(") && strings.HasSuffix(name, ")") && !strings.Contains(name, "/") {
			name = name[1 : len(name)-1]
		}
		if err := s.addProcess(filepath.Base(name), strings.HasPrefix(f[1], "Z"), rss, 1024); err != nil {
			return err
		}
	}
	if s.Processes == 0 {
		return local("host", "invalid_process_counters")
	}
	return nil
}
