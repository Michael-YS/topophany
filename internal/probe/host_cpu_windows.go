package probe

import (
	"context"
	"math"
	"runtime"
	"syscall"
	"unsafe"
)

var getSystemTimes = hostKernel.NewProc("GetSystemTimes")
var groupCount = hostKernel.NewProc("GetActiveProcessorGroupCount")
var currentThread = hostKernel.NewProc("GetCurrentThread")
var setThreadGroup = hostKernel.NewProc("SetThreadGroupAffinity")

type windowsTicks struct{ idle, kernel, user uint64 }
type windowsGroupAffinity struct {
	Mask     uintptr
	Group    uint16
	Reserved [3]uint16
}

func readWindowsTimes() (windowsTicks, error) {
	var idle, kernel, user syscall.Filetime
	ok, _, _ := getSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if ok == 0 {
		return windowsTicks{}, hostReadError("cpu")
	}
	ticks := func(t syscall.Filetime) uint64 { return uint64(t.HighDateTime)<<32 | uint64(t.LowDateTime) }
	return windowsTicks{ticks(idle), ticks(kernel), ticks(user)}, nil
}
func windowsSystemTimes(ctx context.Context) (windowsTicks, error) {
	groups, _, _ := groupCount.Call()
	if groups == 0 || groups > 64 {
		return windowsTicks{}, hostReadError("cpu")
	}
	if groups == 1 {
		return readWindowsTimes()
	}
	type result struct {
		ticks windowsTicks
		err   error
	}
	done := make(chan result, 1)
	go func() {
		// GetSystemTimes covers only the calling thread's primary group. Query
		// every group on a private thread. Retire that thread when the goroutine
		// exits, rather than return changed affinity to the Go scheduler.
		runtime.LockOSThread()
		thread, _, _ := currentThread.Call()
		var sum windowsTicks
		for group := uintptr(0); group < groups; group++ {
			if ctx.Err() != nil {
				done <- result{err: local("host", "host_sampling_canceled")}
				return
			}
			affinity := windowsGroupAffinity{Mask: 1, Group: uint16(group)}
			ok, _, _ := setThreadGroup.Call(thread, uintptr(unsafe.Pointer(&affinity)), 0)
			if ok == 0 {
				done <- result{err: hostReadError("cpu")}
				return
			}
			tick, e := readWindowsTimes()
			if e != nil {
				done <- result{err: e}
				return
			}
			if tick.idle > math.MaxUint64-sum.idle || tick.kernel > math.MaxUint64-sum.kernel || tick.user > math.MaxUint64-sum.user {
				done <- result{err: local("host", "invalid_cpu_counters")}
				return
			}
			sum.idle += tick.idle
			sum.kernel += tick.kernel
			sum.user += tick.user
		}
		done <- result{ticks: sum}
	}()
	select {
	case <-ctx.Done():
		return windowsTicks{}, local("host", "host_sampling_canceled")
	case r := <-done:
		return r.ticks, r.err
	}
}
func windowsCPUPercent(a, b windowsTicks) (float64, error) {
	if b.idle < a.idle || b.kernel < a.kernel || b.user < a.user {
		return 0, local("host", "invalid_cpu_counters")
	}
	kernel, user, idle := b.kernel-a.kernel, b.user-a.user, b.idle-a.idle
	if user > math.MaxUint64-kernel {
		return 0, local("host", "invalid_cpu_counters")
	}
	total := kernel + user
	if total == 0 || idle > total {
		return 0, local("host", "invalid_cpu_counters")
	}
	return percentage(total-idle, total), nil
}
