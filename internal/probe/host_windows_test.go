package probe

import (
	"context"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsCPUCounterAccounting(t *testing.T) {
	a := windowsTicks{idle: 100, kernel: 200, user: 300}
	b := windowsTicks{idle: 140, kernel: 260, user: 340}
	value, e := windowsCPUPercent(a, b)
	if e != nil || value != 60 {
		t.Fatal(value, e)
	} // kernel includes idle
	for _, bad := range []windowsTicks{{idle: 99, kernel: 260, user: 340}, {idle: 100, kernel: 199, user: 340}, a, {idle: 1000, kernel: 260, user: 340}} {
		if _, e = windowsCPUPercent(a, bad); e == nil {
			t.Fatal("invalid CPU counters accepted", bad)
		}
	}
	if _, e = windowsCPUPercent(windowsTicks{}, windowsTicks{kernel: math.MaxUint64, user: 1}); e == nil {
		t.Fatal("overflow accepted")
	}
}
func TestWindowsNativeHostResources(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	name := filepath.Base(exe)
	stem := strings.TrimSuffix(name, ".exe")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sample, e := sampleHostRuntime(ctx, []string{name, stem})
	if e != nil {
		t.Fatal(e)
	}
	if sample.MemoryTotal == 0 || sample.MemoryAvailable > sample.MemoryTotal || sample.CPUPercent < 0 || sample.CPUPercent > 100 || sample.Processes < 1 {
		t.Fatal(sample.HostSample)
	}
	for _, key := range []string{name, stem} {
		match := sample.matches[key]
		if match.count < 1 || match.rss == 0 {
			t.Fatalf("current process missing (%q): %+v", key, match)
		}
	}
	disk, e := readDisk(os.TempDir())
	if e != nil || disk.Total == 0 || disk.Free != disk.Available || disk.Available > disk.Total || disk.Basis != "windows_caller_quota" {
		t.Fatal(disk, e)
	}
	c := model.Config{Schema: 1, Project: "windows-native-test", Nodes: []model.Node{{ID: "windows", Name: "Windows"}}, Observers: []model.Observer{{ID: "native", Node: "windows", Context: "Windows native"}}, Checks: []model.Check{{ID: "host", Observer: "native", Kind: "host", Host: &model.HostOptions{DiskPath: filepath.VolumeName(os.TempDir()) + "/"}}}}
	config.Defaults(&c)
	if e = config.Validate(c); e != nil {
		t.Fatal(e)
	}
	observation := NewRunner(c, "native").Run(ctx, c, c.Checks[0])
	if observation.Outcome != "ok" || observation.Facts["os"] != "windows" || observation.Facts["memory_basis"] != "windows_AvailPhys" || observation.Facts["disk_basis"] != "windows_caller_quota" {
		t.Fatal(observation)
	}
	t.Logf("native Windows counters: CPU %s%%, memory %s, disk %s, processes %s", observation.Facts["cpu_used_percent"], observation.Facts["memory_total_bytes"], observation.Facts["disk_total_bytes"], observation.Facts["process_count"])
}
func TestWindowsHostSamplingCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := sampleHostRuntime(ctx, nil); e == nil {
		t.Fatal("canceled sampler ran")
	}
}
