package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestKilledPublisherExpiresAndReleasesLock(t *testing.T) {
	if path := os.Getenv("TOPO_MONITOR_TEST_CHILD"); path != "" {
		c := testConfig()
		c.Revision = "fixture"
		c.Output = path
		out, e := OpenOutput(path, c.Name)
		if e != nil {
			t.Fatal(e)
		}
		now := time.Now().UTC()
		if e = out.Write(Build(c, hostInput(now), now), time.Second, true); e != nil {
			t.Fatal(e)
		}
		fmt.Println("ready")
		time.Sleep(time.Hour)
		return
	}
	path := filepath.Join(t.TempDir(), "metrics.json")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestKilledPublisherExpiresAndReleasesLock$")
	cmd.Env = append(os.Environ(), "TOPO_MONITOR_TEST_CHILD="+path)
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	line, e := bufio.NewReader(stdout).ReadString('\n')
	if e != nil || line != "ready\n" {
		t.Fatal(line, e)
	}
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	cmd.Wait()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var p Panel
	if e = json.Unmarshal(b, &p); e != nil {
		t.Fatal(e)
	}
	if p.Lifecycle != "running" {
		t.Fatal("crashed process wrote orderly stop", p)
	}
	expired := Current(p, p.GeneratedAt.Add(31*time.Second))
	if expired.Lifecycle != "publisher_expired" || expired.Rows[0].Value != nil {
		t.Fatal(expired)
	}
	out, e := OpenOutput(path, "desktop")
	if e != nil {
		t.Fatal("crash retained lock", e)
	}
	out.Close()
}
