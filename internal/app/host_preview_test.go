package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHostPreviewCommandsShowSyntheticResources(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "host-resources.json")
	now := time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)
	args := []string{"preview", "--config", path, "--lang", "zh"}
	out, err := at(now, append(args, "scenarios", "--json")...)
	var list []struct {
		Index    int
		ID, Name string
	}
	if err != nil || json.Unmarshal([]byte(out), &list) != nil {
		t.Fatal(err, out)
	}
	index := -1
	for _, sc := range list {
		if sc.ID == "host-cpu-high" {
			index = sc.Index
			if sc.Name != "主机 CPU 高占用" {
				t.Fatal(sc)
			}
		}
	}
	if index < 0 {
		t.Fatal("host scenario missing", out)
	}
	for _, command := range [][]string{{"show", "checks", "host-resources"}, {"history", "host-resources", "--json"}} {
		out, err = at(now, append(append(args, "--scenario", fmt.Sprint(index)), command...)...)
		if err != nil || !strings.Contains(out, "cpu_used_percent") || !strings.Contains(out, "host_resource_threshold") || !strings.Contains(out, "host_resources") {
			t.Fatal(command, err, out)
		}
	}
	out, err = at(now, append(append(args, "--scenario", fmt.Sprint(index)), "status", "--json")...)
	if exitCode(err) != ExitAttention || !strings.Contains(out, "policy") {
		t.Fatal(err, out)
	}
}
