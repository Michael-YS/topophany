package probe

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// HostSample contains bounded aggregate counters, never process arguments,
// environment variables, user names or a process inventory.
type HostSample struct {
	CPUPercent                   float64
	CPUSampleMS                  int64
	MemoryTotal, MemoryAvailable uint64
	MemoryBasis                  string
	Disk                         hostDisk
	Processes, Zombies, Matching int
	MatchingRSS                  uint64
}

type hostDisk struct {
	Total, Free, Available uint64
	Inodes, InodesFree     uint64
}

func (r Runner) host(ctx context.Context, x model.Check, o *model.Observation) error {
	h := model.HostOptions{}
	if x.Host != nil {
		h = *x.Host
	}
	if h.DiskPath == "" {
		h.DiskPath = "/"
	}
	collect := r.HostSample
	if collect == nil {
		collect = r.sampleHost
	}
	s, err := collect(ctx, h)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return local("host", "host_sampling_canceled")
	}
	if math.IsNaN(s.CPUPercent) || math.IsInf(s.CPUPercent, 0) || s.CPUPercent < 0 || s.CPUPercent > 100 || s.MemoryTotal == 0 || s.MemoryAvailable > s.MemoryTotal || s.Disk.Total == 0 || s.Disk.Free > s.Disk.Total || s.Disk.Available > s.Disk.Free || s.Disk.InodesFree > s.Disk.Inodes {
		return local("host", "invalid_host_counters")
	}
	memoryUsed := s.MemoryTotal - s.MemoryAvailable
	diskUsed := s.Disk.Total - s.Disk.Free
	memoryPercent := percentage(memoryUsed, s.MemoryTotal)
	// As with df, reserved blocks are not counted as available to this worker.
	diskPercent := percentage(diskUsed, diskUsed+s.Disk.Available)
	put := func(k string, n uint64) { o.Facts[k] = strconv.FormatUint(n, 10) }
	pct := func(k string, n float64) { o.Facts[k] = strconv.FormatFloat(n, 'f', 2, 64) }
	o.Facts["os"] = runtime.GOOS
	o.Facts["resource_scope"] = "worker_OS_view; not_container_quota"
	pct("cpu_used_percent", s.CPUPercent)
	o.Facts["cpu_logical_count"] = strconv.Itoa(runtime.NumCPU())
	o.Facts["cpu_sample_ms"] = strconv.FormatInt(s.CPUSampleMS, 10)
	put("memory_total_bytes", s.MemoryTotal)
	put("memory_available_bytes", s.MemoryAvailable)
	put("memory_used_bytes", memoryUsed)
	pct("memory_used_percent", memoryPercent)
	o.Facts["memory_basis"] = s.MemoryBasis
	o.Facts["disk_path"] = h.DiskPath
	put("disk_total_bytes", s.Disk.Total)
	put("disk_used_bytes", diskUsed)
	put("disk_available_bytes", s.Disk.Available)
	pct("disk_used_percent", diskPercent)
	put("disk_inodes_total", s.Disk.Inodes)
	put("disk_inodes_free", s.Disk.InodesFree)
	o.Facts["process_scope"] = "visible_processes; non_atomic_snapshot"
	o.Facts["process_count"] = strconv.Itoa(s.Processes)
	o.Facts["process_zombie_count"] = strconv.Itoa(s.Zombies)
	if h.ProcessName != "" {
		o.Facts["process_name"] = h.ProcessName
		o.Facts["process_matching_count"] = strconv.Itoa(s.Matching)
		put("process_matching_rss_bytes", s.MatchingRSS)
	}
	violations := []string{}
	for _, v := range []struct {
		name       string
		value, max float64
	}{{"cpu", s.CPUPercent, h.MaxCPUPercent}, {"memory", memoryPercent, h.MaxMemoryPercent}, {"disk", diskPercent, h.MaxDiskPercent}} {
		if v.max > 0 && v.value > v.max {
			violations = append(violations, v.name)
		}
	}
	if s.Matching < h.MinProcesses || h.MaxProcesses > 0 && s.Matching > h.MaxProcesses {
		violations = append(violations, "process_count")
	}
	o.Stage = "host"
	o.Outcome = "ok"
	o.Detail = "local host resource counters collected; configured assertions passed"
	if len(violations) > 0 {
		o.Outcome = "policy"
		o.Code = "host_resource_threshold"
		o.Facts["violations"] = strings.Join(violations, ",")
		o.Detail = "local host resource assertions violated: " + strings.Join(violations, ", ")
	}
	return nil
}

func percentage(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

func counter(s string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, local("host", "invalid_host_counters")
	}
	return n, nil
}

func bytesFromUnits(n, unit uint64) (uint64, error) {
	if unit == 0 || n > math.MaxUint64/unit {
		return 0, local("host", "invalid_host_counters")
	}
	return n * unit, nil
}

func hostReadError(part string) error {
	return local("host", fmt.Sprintf("host_%s_unavailable", part))
}
