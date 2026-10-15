//go:build linux || darwin

package probe

import "syscall"

func sampleDisk(path string) (hostDisk, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return hostDisk{}, hostReadError("disk")
	}
	if st.Bsize <= 0 {
		return hostDisk{}, local("host", "invalid_host_counters")
	}
	unit := uint64(st.Bsize)
	total, e1 := bytesFromUnits(st.Blocks, unit)
	free, e2 := bytesFromUnits(st.Bfree, unit)
	available, e3 := bytesFromUnits(st.Bavail, unit)
	if e1 != nil || e2 != nil || e3 != nil {
		return hostDisk{}, local("host", "invalid_host_counters")
	}
	return hostDisk{Total: total, Free: free, Available: available, Inodes: st.Files, InodesFree: st.Ffree}, nil
}
