package probe

import (
	"context"
	"errors"
	"math"
	"strings"
	"syscall"
	"unsafe"
)

var processMemoryInfo = hostKernel.NewProc("K32GetProcessMemoryInfo")

type windowsProcessMemory struct {
	Size, PageFaults                                                                                             uint32
	PeakWorkingSet, WorkingSet, PeakPagedPool, PagedPool, PeakNonPagedPool, NonPagedPool, Pagefile, PeakPagefile uintptr
}

func windowsProcessRSS(pid uint32) (uint64, error) {
	// The modern API needs only query-limited rights, not process control or
	// reading another process's arguments/environment/address space.
	const queryLimitedInformation = 0x1000
	handle, e := syscall.OpenProcess(queryLimitedInformation, false, pid)
	if e != nil {
		return 0, e
	}
	defer syscall.CloseHandle(handle)
	var memory windowsProcessMemory
	memory.Size = uint32(unsafe.Sizeof(memory))
	ok, _, e := processMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&memory)), uintptr(memory.Size))
	if ok == 0 {
		return 0, e
	}
	return uint64(memory.WorkingSet), nil
}
func windowsProcesses(ctx context.Context, s *hostRuntime) error {
	handle, e := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if e != nil {
		return hostReadError("process")
	}
	defer syscall.CloseHandle(handle)
	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for e = syscall.Process32First(handle, &entry); e == nil; e = syscall.Process32Next(handle, &entry) {
		if ctx.Err() != nil {
			return local("host", "host_sampling_canceled")
		}
		s.Processes++
		if s.Processes > 65536 {
			return local("host", "host_read_budget_exceeded")
		}
		name := syscall.UTF16ToString(entry.ExeFile[:])
		stem := name
		if strings.HasSuffix(strings.ToLower(stem), ".exe") {
			stem = stem[:len(stem)-4]
		}
		matches := []string{}
		for requested := range s.matches {
			if strings.EqualFold(requested, name) || strings.EqualFold(requested, stem) {
				matches = append(matches, requested)
			}
		}
		if len(matches) == 0 {
			continue
		}
		rss, err := windowsProcessRSS(entry.ProcessID)
		if errors.Is(err, syscall.Errno(87)) || errors.Is(err, syscall.Errno(1168)) {
			continue
		} // exited since the snapshot
		if err != nil {
			return hostReadError("process")
		}
		for _, requested := range matches {
			match := s.matches[requested]
			if rss > math.MaxUint64-match.rss {
				return local("host", "invalid_process_counters")
			}
			match.count++
			match.rss += rss
			s.matches[requested] = match
		}
	}
	if !errors.Is(e, syscall.ERROR_NO_MORE_FILES) {
		return hostReadError("process")
	}
	return nil
}
