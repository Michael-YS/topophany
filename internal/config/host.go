package config

import (
	"errors"
	"math"
	"path"
	"strings"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func validateHost(x model.Check, c model.Config) error {
	if x.Kind != "host" {
		if x.Host != nil {
			return errors.New("host options require kind host")
		}
		return nil
	}
	if x.Endpoint != "" || x.Target != "" || x.Path != "" || x.Link != "" || x.BindIP != "" || x.ResolveIP != "" || x.ServerName != "" || x.CAFile != "" {
		return errors.New("host checks are local and cannot specify endpoint, target, path, link or dial options")
	}
	// Local resource evidence may describe only a service on this observer's node.
	if x.Service != "" {
		node := ""
		for _, o := range c.Observers {
			if o.ID == x.Observer {
				node = o.Node
			}
		}
		for _, s := range c.Services {
			if s.ID == x.Service && (node == "" || s.Node != node) {
				return errors.New("host check service must be on the observer's node")
			}
		}
	}
	if x.TimeoutSeconds < 3 {
		return errors.New("host checks need timeout_seconds >= 3 for CPU sampling")
	}
	if x.Host == nil {
		return nil
	}
	h := x.Host
	if h.DiskPath != "" && (!path.IsAbs(h.DiskPath) || path.Clean(h.DiskPath) != h.DiskPath || len(h.DiskPath) > 1024 || secure.Text(h.DiskPath) != h.DiskPath) {
		return errors.New("host.disk_path must be a clean absolute local filesystem path (up to 1024 bytes)")
	}
	for _, n := range []float64{h.MaxCPUPercent, h.MaxMemoryPercent, h.MaxDiskPercent} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
			return errors.New("host percentage thresholds must be 0..100 (0 disables the assertion)")
		}
	}
	if len(h.ProcessName) > 15 || secure.Text(h.ProcessName) != h.ProcessName || strings.ContainsAny(h.ProcessName, "/\\") || strings.TrimSpace(h.ProcessName) != h.ProcessName {
		return errors.New("host.process_name must be an exact process name of at most 15 bytes, without paths or control characters")
	}
	if h.MinProcesses < 0 || h.MaxProcesses < 0 || h.MinProcesses > 65536 || h.MaxProcesses > 65536 || h.MaxProcesses > 0 && h.MinProcesses > h.MaxProcesses {
		return errors.New("host process bounds must be 0..65536 with min <= enabled max")
	}
	if h.ProcessName == "" && (h.MinProcesses != 0 || h.MaxProcesses != 0) {
		return errors.New("host process bounds require process_name")
	}
	return nil
}
