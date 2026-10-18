package probe

import (
	"bytes"
	"context"
	"encoding/binary"
	"time"
)

// Darwin's 64-bit kinfo_proc ABI is shared by arm64 and amd64. Only the
// process status, PID and kernel command name are read; the rest is discarded.
const darwinProcSize = 648

func darwinCPUPercent(before, after [4]uint32) (float64, error) {
	var total, idle uint64
	for i := range before {
		delta := uint64(after[i] - before[i]) // kernel tick counters wrap at 32 bits
		total += delta
		if i == 2 {
			idle = delta
		}
	}
	if total == 0 {
		return 0, local("host", "invalid_cpu_counters")
	}
	return percentage(total-idle, total), nil
}

func darwinAvailable(raw []byte, page, total uint64) (uint64, error) {
	if len(raw) < 96 {
		return 0, local("host", "invalid_memory_counters")
	}
	free := uint64(binary.LittleEndian.Uint32(raw[0:4]))
	inactive := uint64(binary.LittleEndian.Uint32(raw[8:12]))
	speculative := uint64(binary.LittleEndian.Uint32(raw[92:96]))
	if speculative > free {
		return 0, local("host", "invalid_memory_counters")
	}
	// vm_stat prints free excluding speculative pages. Mach's free_count
	// includes them, so normalize before summing the three available classes.
	available, err := bytesFromUnits((free-speculative)+inactive+speculative, page)
	if err != nil || available > total {
		return 0, local("host", "invalid_memory_counters")
	}
	return available, nil
}

func parseDarwinProcessTable(raw []byte, s *hostRuntime, rss func(int32) (uint64, error)) error {
	if len(raw) == 0 || len(raw) > 8<<20 || len(raw)%darwinProcSize != 0 || len(raw)/darwinProcSize > 65536 {
		return local("host", "invalid_process_counters")
	}
	for len(raw) > 0 {
		p := raw[:darwinProcSize]
		raw = raw[darwinProcSize:]
		pid := int32(binary.LittleEndian.Uint32(p[40:44]))
		if pid < 0 {
			return local("host", "invalid_process_counters")
		}
		name := p[243:260]
		end := bytes.IndexByte(name, 0)
		if end < 0 {
			return local("host", "invalid_process_counters")
		}
		command := string(name[:end])
		zombie := p[36] == 5 // SZOMB
		var resident uint64
		if _, selected := s.matches[command]; selected && !zombie {
			var err error
			resident, err = rss(pid)
			if err != nil {
				return err
			}
		}
		if err := s.addProcess(command, zombie, resident, 1); err != nil {
			return err
		}
	}
	return nil
}

// The kernel can return cached CPU ticks to ordinary processes, with a global
// randomized request limit per second. Do not report zero CPU for that case.
// Retry a new 200 ms window after a cooldown, bounded to 2.6 seconds total.
func sampleDarwinCPU(ctx context.Context, read func() ([4]uint32, error), wait func(context.Context, time.Duration) error) (float64, error) {
	for attempt := 0; attempt < 3; attempt++ {
		before, err := read()
		if err != nil {
			return 0, err
		}
		if err := wait(ctx, 200*time.Millisecond); err != nil {
			return 0, err
		}
		after, err := read()
		if err != nil {
			return 0, err
		}
		if before != after {
			return darwinCPUPercent(before, after)
		}
		if attempt < 2 {
			if err := wait(ctx, time.Second); err != nil {
				return 0, err
			}
		}
	}
	return 0, local("host", "invalid_cpu_counters")
}

func waitDarwinSample(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return local("host", "host_sampling_canceled")
	case <-timer.C:
		return nil
	}
}
