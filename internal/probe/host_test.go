package probe

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func hostFixture() HostSample {
	return HostSample{CPUPercent: 25, CPUSampleMS: 200, MemoryTotal: 1000, MemoryAvailable: 250,
		MemoryBasis: "fixture", Disk: hostDisk{Total: 1000, Free: 200, Available: 100, Inodes: 100, InodesFree: 50},
		Processes: 20, Zombies: 1, Matching: 2, MatchingRSS: 4096}
}

func TestHostAssertionsAndDeliverableEvidence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		h          model.HostOptions
		violations string
	}{
		{"collect only", model.HostOptions{}, ""},
		{"threshold equality passes", model.HostOptions{MaxCPUPercent: 25, MaxMemoryPercent: 75, MaxDiskPercent: 90}, ""},
		{"all thresholds", model.HostOptions{MaxCPUPercent: 20, MaxMemoryPercent: 70, MaxDiskPercent: 85, ProcessName: "topo", MinProcesses: 3}, "cpu,memory,disk,process_count"},
		{"too many processes", model.HostOptions{ProcessName: "topo", MaxProcesses: 1}, "process_count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := model.Check{ID: "host", Observer: "home", Kind: "host", TimeoutSeconds: 8, Host: &tc.h}
			r := Runner{HostSample: func(_ context.Context, h model.HostOptions) (HostSample, error) {
				if h.DiskPath != "/" {
					t.Fatal("default filesystem", h)
				}
				return hostFixture(), nil
			}}
			o := r.Run(context.Background(), testkit.Config(), x)
			want := "ok"
			if tc.violations != "" {
				want = "policy"
			}
			if o.Outcome != want || o.Facts["violations"] != tc.violations || o.Evidence != "reported" || o.Connectivity || o.Facts["response_proof"] != "" || o.PathEvidence != "local_host_runtime_reported" {
				t.Fatal(o)
			}
			if o.Facts["disk_used_percent"] != "88.89" || o.Facts["memory_used_percent"] != "75.00" {
				t.Fatal("resource accounting", o.Facts)
			}
			if code := config.ObservationBounds(o, 13*time.Second); code != "" {
				t.Fatal(code, o)
			}
		})
	}
}

func TestHostReadErrorsNeverBecomeTargetFailures(t *testing.T) {
	x := model.Check{ID: "host", Observer: "home", Kind: "host", TimeoutSeconds: 8}
	r := Runner{HostSample: func(context.Context, model.HostOptions) (HostSample, error) {
		return HostSample{}, hostReadError("processes")
	}}
	o := r.Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "error" || o.Code != "host_processes_unavailable" || o.Connectivity {
		t.Fatal(o)
	}
	for _, invalid := range []func(*HostSample){
		func(s *HostSample) { s.CPUPercent = math.NaN() },
		func(s *HostSample) { s.MemoryAvailable = s.MemoryTotal + 1 },
		func(s *HostSample) { s.Disk.Available = s.Disk.Free + 1 },
	} {
		r.HostSample = func(context.Context, model.HostOptions) (HostSample, error) {
			s := hostFixture()
			invalid(&s)
			return s, nil
		}
		if o := r.Run(context.Background(), testkit.Config(), x); o.Outcome != "error" {
			t.Fatal(o)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.HostSample = func(context.Context, model.HostOptions) (HostSample, error) { return hostFixture(), nil }
	if o := r.Run(ctx, testkit.Config(), x); o.Outcome != "error" {
		t.Fatal(o)
	}
}

const darwinVMFixture = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                100.
Pages inactive:                            200.
Pages speculative:                          50.
Pages active:                              600.
Pages wired down:                          100.
Pages occupied by compressor:               25.
`

func TestDarwinHostOutputFixtures(t *testing.T) {
	commands := []string{}
	command := func(_ context.Context, name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		switch name {
		case "/usr/bin/top":
			return []byte("CPU usage: 1.0% user, 1.0% sys, 98.0% idle\nCPU usage: 10.0% user, 15.0% sys, 75.0% idle\n"), nil
		case "/usr/sbin/sysctl":
			return []byte("16777216\n"), nil
		case "/usr/bin/vm_stat":
			return []byte(darwinVMFixture), nil
		case "/bin/ps":
			return []byte(" 100 S 1024 /Applications/Some App/topo\n101 Z 0 topo\n102 R 2048 /usr/bin/other\n"), nil
		}
		t.Fatal("unexpected system command", name, args)
		return nil, nil
	}
	s, err := sampleDarwinHost(context.Background(), model.HostOptions{DiskPath: "/", ProcessName: "topo"}, command, func(string) (hostDisk, error) { return hostFixture().Disk, nil })
	if err != nil || s.CPUPercent != 25 || s.MemoryAvailable != 350*16384 || s.Processes != 3 || s.Zombies != 1 || s.Matching != 1 || s.MatchingRSS != 1048576 {
		t.Fatal(s, err)
	}
	if len(commands) != 4 || commands[3] != "/bin/ps -ww -axo pid=,stat=,rss=,comm=" {
		t.Fatal(commands)
	}
	for _, top := range []string{"CPU usage: 0% user, 0% sys, 100% idle\n", "CPU usage: 0% user, 0% sys, 100% idle\nCPU usage: 0% user, 0% sys, 101% idle\n"} {
		if _, err := parseDarwinCPU(top); err == nil {
			t.Fatal("invalid CPU sample accepted")
		}
	}
	for _, vm := range []string{strings.Replace(darwinVMFixture, "16384", "4096", 1), darwinVMFixture} {
		if _, err := parseDarwinMemory(vm, 1); err == nil {
			t.Fatal("impossible memory counters accepted")
		}
	}
	for _, ps := range []string{"", "100 R bad topo", "100 R 10"} {
		if err := parseDarwinProcesses(ps, "topo", &HostSample{}); err == nil {
			t.Fatal("malformed process snapshot accepted")
		}
	}
	for _, failed := range []string{"/usr/bin/top", "/usr/sbin/sysctl", "/usr/bin/vm_stat", "/bin/ps"} {
		_, err := sampleDarwinHost(context.Background(), model.HostOptions{}, func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name == failed {
				return nil, hostReadError("command")
			}
			return command(ctx, name, args...)
		}, func(string) (hostDisk, error) { return hostFixture().Disk, nil })
		if err == nil {
			t.Fatal("command failure ignored", failed)
		}
	}
}
