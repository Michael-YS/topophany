package probe

import (
	"context"
	"encoding/binary"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Use the syscall package's libc entry point, including its stack switching
// and pointer lifetime handling. The linker explicitly supports this symbol.
//
//go:linkname darwinCall6 syscall.syscall6
//go:uintptrescapes
func darwinCall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, uintptr, syscall.Errno)

//go:cgo_import_dynamic topo_mach_host_self mach_host_self "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic topo_host_statistics host_statistics "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic topo_host_statistics64 host_statistics64 "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic topo_sysctl sysctl "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic topo_proc_pidinfo proc_pidinfo "/usr/lib/libSystem.B.dylib"

func hostSelfPC() uintptr
func hostStatisticsPC() uintptr
func hostStatistics64PC() uintptr
func hostSysctlPC() uintptr
func hostProcInfoPC() uintptr

// Retain one host send right for the process lifetime rather than acquiring
// a fresh right on every sample.
var darwinHostPort = sync.OnceValue(func() uintptr {
	port, _, _ := darwinCall6(hostSelfPC(), 0, 0, 0, 0, 0, 0)
	return port
})

func darwinCPUTicks() ([4]uint32, error) {
	var ticks [4]uint32
	count := uint32(len(ticks))
	r, _, _ := darwinCall6(hostStatisticsPC(), darwinHostPort(), 3, uintptr(unsafe.Pointer(&ticks)), uintptr(unsafe.Pointer(&count)), 0, 0)
	if r != 0 || count != 4 {
		return ticks, hostReadError("cpu")
	}
	return ticks, nil
}

func sampleHostRuntime(ctx context.Context, names []string) (hostRuntime, error) {
	s := newHostRuntime(names)
	before, err := darwinCPUTicks()
	if err != nil {
		return s, err
	}
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return s, local("host", "host_sampling_canceled")
	case <-timer.C:
	}
	after, err := darwinCPUTicks()
	if err != nil {
		return s, err
	}
	s.CPUPercent, err = darwinCPUPercent(before, after)
	if err != nil {
		return s, err
	}
	s.CPUSampleMS = 200
	mem, err := syscall.Sysctl("hw.memsize")
	// Sysctl strips one trailing zero byte, including from binary counters.
	if err != nil || len(mem) < 7 || len(mem) > 8 {
		return s, hostReadError("memory")
	}
	var memory [8]byte
	copy(memory[:], mem)
	s.MemoryTotal = binary.LittleEndian.Uint64(memory[:])
	if s.MemoryTotal == 0 {
		return s, local("host", "invalid_memory_counters")
	}
	// Request the stable revision-zero VM prefix; later kernel extensions are
	// unnecessary. Its count is in 32-bit words, while its counters need alignment.
	var vm [12]uint64
	count := uint32(24)
	r, _, _ := darwinCall6(hostStatistics64PC(), darwinHostPort(), 4, uintptr(unsafe.Pointer(&vm)), uintptr(unsafe.Pointer(&count)), 0, 0)
	if r != 0 || count < 24 {
		return s, hostReadError("memory")
	}
	page := uint64(syscall.Getpagesize())
	s.MemoryAvailable, err = darwinAvailable(unsafe.Slice((*byte)(unsafe.Pointer(&vm)), 96), page, s.MemoryTotal)
	if err != nil {
		return s, err
	}
	s.MemoryBasis = "macos_estimated_free_inactive_speculative"
	raw, err := darwinProcesses(ctx)
	if err != nil {
		return s, err
	}
	err = parseDarwinProcessTable(raw, &s, func(pid int32) (uint64, error) {
		if ctx.Err() != nil {
			return 0, local("host", "host_sampling_canceled")
		}
		var task [12]uint64 // proc_taskinfo: resident_size is the second uint64
		n, _, _ := darwinCall6(hostProcInfoPC(), uintptr(pid), 4, 0, uintptr(unsafe.Pointer(&task)), 96, 0)
		if n != 96 {
			return 0, hostReadError("processes")
		}
		return task[1], nil
	})
	return s, err
}

func darwinProcesses(ctx context.Context) ([]byte, error) {
	mib := [3]int32{1, 14, 0} // CTL_KERN, KERN_PROC, KERN_PROC_ALL
	// A process may appear between the sizing and reading calls. Retry within
	// the original 8 MiB read budget, with bounded attempts and spare records.
	for range 3 {
		if ctx.Err() != nil {
			return nil, local("host", "host_sampling_canceled")
		}
		var size uintptr
		_, _, err := darwinCall6(hostSysctlPC(), uintptr(unsafe.Pointer(&mib)), 3, 0, uintptr(unsafe.Pointer(&size)), 0, 0)
		if err != 0 {
			return nil, hostReadError("processes")
		}
		const budget = 8 << 20
		if size == 0 || size > budget {
			return nil, local("host", "invalid_process_counters")
		}
		capacity := min(size+16*darwinProcSize, budget)
		data := make([]byte, capacity)
		size = capacity
		_, _, err = darwinCall6(hostSysctlPC(), uintptr(unsafe.Pointer(&mib)), 3, uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
		if err == syscall.ENOMEM {
			continue
		}
		if err != 0 || size > capacity {
			return nil, hostReadError("processes")
		}
		return data[:size], nil
	}
	return nil, hostReadError("processes")
}
