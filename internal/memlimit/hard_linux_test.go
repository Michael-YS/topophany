package memlimit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestCgroupConfiguration(t *testing.T) {
	makeGroup := func(t *testing.T) (string, string) {
		root := t.TempDir()
		rel := "/topo"
		dir := filepath.Join(root, rel)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]string{"memory.max": "max", "memory.swap.max": "max", "cgroup.procs": strconv.Itoa(os.Getpid())} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return root, dir
	}
	t.Run("private", func(t *testing.T) {
		root, dir := makeGroup(t)
		if err := configureCgroup(root, "/topo", 128<<20, os.Getpid()); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "memory.max"))
		if string(b) != "134217728" {
			t.Fatal(string(b))
		}
		b, _ = os.ReadFile(filepath.Join(dir, "memory.swap.max"))
		if string(b) != "0" {
			t.Fatal(string(b))
		}
	})
	t.Run("shared", func(t *testing.T) {
		root, dir := makeGroup(t)
		os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte("1\n2\n"), 0600)
		if err := configureCgroup(root, "/topo", 128<<20, os.Getpid()); err == nil {
			t.Fatal("modified shared group")
		}
	})
	t.Run("ancestor", func(t *testing.T) {
		root, dir := makeGroup(t)
		os.WriteFile(filepath.Join(root, "memory.max"), []byte("67108864"), 0600)
		os.WriteFile(filepath.Join(root, "memory.swap.max"), []byte("0"), 0600)
		os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte("1\n2\n"), 0600)
		if err := configureCgroup(root, "/topo", 128<<20, os.Getpid()); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "memory.max"))
		if string(b) != "max" {
			t.Fatal("raised existing ancestor cap")
		}
	})
	t.Run("missing-controller", func(t *testing.T) {
		root, dir := makeGroup(t)
		os.Remove(filepath.Join(dir, "memory.max"))
		if err := configureCgroup(root, "/topo", 128<<20, os.Getpid()); err == nil {
			t.Fatal("absent controller accepted")
		}
	})
	for _, raw := range []string{"1:memory:/topo", "0::/../../other", "0::relative"} {
		if _, e := unifiedPath(raw); e == nil {
			t.Fatal("unsafe path accepted", raw)
		}
	}
}

// Opt-in native Linux acceptance. TOPO_TEST_CGROUP must be a writable cgroup v2
// parent with +memory enabled. The test creates/removes only a private child.
// The workload must be OOM-killed, proving that the OS cap works even with
// deliberately retained live objects that Go's soft target cannot reclaim.
func TestKernelMemoryMax(t *testing.T) {
	if os.Getenv("TOPO_MEMORY_CHILD") == "1" {
		if _, err := Apply(32<<20, 64<<20); err != nil {
			t.Fatal(err)
		}
		retained := [][]byte{}
		for i := 0; i < 256; i++ {
			b := make([]byte, 1<<20)
			for j := 0; j < len(b); j += 4096 {
				b[j] = 1
			}
			retained = append(retained, b)
		}
		t.Fatalf("hard cap failed: retained %d MiB", len(retained))
	}
	parent := os.Getenv("TOPO_TEST_CGROUP")
	if parent == "" {
		t.Skip("set TOPO_TEST_CGROUP for a native kernel OOM test")
	}
	dir, err := os.MkdirTemp(parent, "topo-memory-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dir)
	for name, value := range map[string]string{"memory.max": "134217728", "memory.swap.max": "0", "memory.oom.group": "1"} {
		if err = writeControl(filepath.Join(dir, name), value); err != nil {
			t.Fatal(err)
		}
	}
	group, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestKernelMemoryMax$")
	cmd.Env = append(os.Environ(), "TOPO_MEMORY_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(group.Fd())}
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("workload survived hard cap", string(output))
	}
	exit, ok := err.(*exec.ExitError)
	if !ok || !exit.Sys().(syscall.WaitStatus).Signaled() || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("not a kernel OOM kill: %v %s", err, output)
	}
	events, err := os.ReadFile(filepath.Join(dir, "memory.events"))
	if err != nil {
		t.Fatal(err)
	}
	killed := false
	for line := range strings.SplitSeq(string(events), "\n") {
		if n, ok := strings.CutPrefix(line, "oom_kill "); ok {
			v, _ := strconv.Atoi(n)
			killed = v > 0
		}
	}
	if !killed {
		t.Fatal("no kernel OOM event", string(events))
	}
}
