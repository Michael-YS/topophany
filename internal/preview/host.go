package preview

import (
	"math"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/probe"
)

// observeHost builds deterministic counters only. It never executes a probe,
// reads /proc, stats a configured path, or inspects the user's real processes.
func (s *Sim) observeHost(x model.Check, at time.Time, sc Scenario, o model.Observation) model.Observation {
	h := model.HostOptions{}
	if x.Host != nil {
		h = *x.Host
	}
	// Load moves as a host's does: CPU jitters from sample to sample with
	// the odd burst, memory drifts over minutes and the disk over hours.
	phase, t := float64(hash(x.ID)%100), float64(at.Unix())
	wave := func(period float64) float64 { return (1 + math.Sin(t/period+phase)) / 2 }
	jitter, burst := unit(x.ID, at.Unix(), 1), unit(x.ID, at.Unix(), 2)
	cpu := 12 + 12*wave(90) + 10*jitter + 18*max(0, burst-.92)/.08
	memory := 40 + 8*wave(600) + 1.5*unit(x.ID, at.Unix(), 3)
	disk := 57 + 3*wave(3600)
	matching := max(1, h.MinProcesses)
	if h.MaxProcesses > 0 {
		matching = min(matching, h.MaxProcesses)
	}
	issue := sc.HostIssue
	if s.nodes[sc.HostNode].ID != "" && s.obsNode[x.Observer] != sc.HostNode {
		issue = ""
	}
	switch issue {
	case "cpu":
		cpu = 94 + 3*wave(90) + 2.5*jitter
	case "memory":
		memory = 95 + 2*wave(600) + 1.5*jitter
	case "disk":
		disk = 98 + wave(3600)
	case "process":
		matching = 0
	}
	// Per-host capacities stay stable across samples and languages.
	seed := hash(x.Observer)
	memoryTotal := uint64(4+(seed%8)*4) << 30
	diskTotal := uint64(64+(seed%8)*64) << 30
	free := uint64(float64(diskTotal) * (100 - disk) / 100)
	os := "linux"
	if strings.Contains(strings.ToLower(x.Observer), "mac") {
		os = "darwin"
	}
	sample := probe.HostSample{
		OS: os, LogicalCPUs: 2 + int(seed%4)*2, CPUPercent: cpu, CPUSampleMS: 200,
		MemoryTotal: memoryTotal, MemoryAvailable: uint64(float64(memoryTotal) * (100 - memory) / 100), MemoryBasis: "synthetic_available",
		Disk:      probe.HostDisk{Total: diskTotal, Free: free, Available: free, Inodes: 1 << 24, InodesFree: uint64(float64(1<<24) * (100 - disk) / 100)},
		Processes: max(180, matching+12), Zombies: 0, Matching: matching, MatchingRSS: uint64(matching) * (48 << 20),
	}
	if x.Observer == "obs-win" {
		sample.OS = "windows" // Synthetic counters only; native collection supports Linux/macOS.
	}
	if sample.OS == "darwin" {
		sample.CPUSampleMS = 1000
	}
	if err := probe.ApplyHostSample(&o, h, sample); err != nil {
		o.Outcome, o.Stage, o.Code = "error", "host", "invalid_host_simulation"
		o.Detail = "synthetic host counters could not be evaluated"
		return o
	}
	o.DurationMS = sample.CPUSampleMS
	o.Facts["simulation"] = "host_resources"
	return o
}
