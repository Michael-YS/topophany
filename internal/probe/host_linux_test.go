package probe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestLinuxCPUAccountingAndReset(t *testing.T) {
	a, err := parseLinuxCPU("cpu 100 20 30 400 50 10 20 5 80 10\ncpu0 1 2 3 4")
	if err != nil || a.total != 635 || a.idle != 450 {
		t.Fatal(a, err)
	}
	got, err := cpuDelta(a, cpuTicks{total: 735, idle: 525})
	if err != nil || got != 25 {
		t.Fatal(got, err)
	}
	for _, b := range []cpuTicks{a, {total: 634, idle: 449}, {total: 735, idle: 449}, {total: 735, idle: 551}} {
		if _, err := cpuDelta(a, b); err == nil {
			t.Fatal("counter reset accepted", b)
		}
	}
	if _, err := parseLinuxCPU("cpu 1 2 broken"); err == nil {
		t.Fatal("malformed CPU accepted")
	}
}

func TestLinuxMemoryAccounting(t *testing.T) {
	total, avail, err := parseLinuxMemory("MemTotal: 1000 kB\nMemFree: 100 kB\nMemAvailable: 250 kB\n")
	if err != nil || total != 1024000 || avail != 256000 {
		t.Fatal(total, avail, err)
	}
	for _, raw := range []string{"MemTotal: 1000 kB", "MemTotal: 1 kB\nMemAvailable: 2 kB", "MemTotal: 1000 B\nMemAvailable: 2 kB", "MemTotal: 18446744073709551615 kB\nMemAvailable: 0 kB"} {
		if _, _, err := parseLinuxMemory(raw); err == nil {
			t.Fatal("invalid memory accepted", raw)
		}
	}
}

func processStat(pid int, name, state string, rss int) string {
	return fmt.Sprintf("%d (%s) %s %s %d 0\n", pid, name, state, strings.Repeat("0 ", 20), rss)
}

func TestLinuxProcessSnapshotAndDisappearingProcess(t *testing.T) {
	root := t.TempDir()
	for i, p := range []struct {
		name, state string
		rss         int
	}{{"topo", "S", 10}, {"topo", "Z", 0}, {"space ) name", "R", 20}} {
		dir := filepath.Join(root, fmt.Sprint(i+1))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(processStat(i+1, p.name, p.state, p.rss)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "999"), 0700); err != nil {
		t.Fatal(err)
	} // Exited before stat read.
	var s HostSample
	if err := linuxProcesses(context.Background(), root, "topo", &s); err != nil {
		t.Fatal(err)
	}
	if s.Processes != 3 || s.Zombies != 1 || s.Matching != 1 || s.MatchingRSS != uint64(10*os.Getpagesize()) {
		t.Fatal(s)
	}
	if name, _, rss, err := parseLinuxProcess(processStat(1, "space ) name", "R", 20)); err != nil || name != "space ) name" || rss != 20 {
		t.Fatal(name, rss, err)
	}
	if _, _, _, err := parseLinuxProcess("1 (bad) S 0"); err == nil {
		t.Fatal("malformed process accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := linuxProcesses(ctx, root, "", &HostSample{}); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestLinuxHostNativeSampling(t *testing.T) {
	s, err := sampleLinuxHost(context.Background(), model.HostOptions{DiskPath: t.TempDir()}, "/proc")
	if err != nil || s.MemoryTotal == 0 || s.Disk.Total == 0 || s.Processes < 1 || s.CPUPercent < 0 || s.CPUPercent > 100 || s.CPUSampleMS < 200 {
		t.Fatal(s, err)
	}
	if _, err := sampleLinuxHost(context.Background(), model.HostOptions{}, t.TempDir()); err == nil {
		t.Fatal("missing procfs accepted")
	}
	if _, err := sampleDisk(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing filesystem accepted")
	}
}
