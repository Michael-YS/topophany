//go:build !linux && !darwin

package probe

import "context"

func sampleHostRuntime(context.Context, []string) (hostRuntime, error) {
	return hostRuntime{}, local("host", "host_platform_unsupported")
}

func readDisk(string) (HostDisk, error) {
	return HostDisk{}, local("host", "host_platform_unsupported")
}
