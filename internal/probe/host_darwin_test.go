package probe

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestDarwinNativeHostMatchesRunningProcess(t *testing.T) {
	name := filepath.Base(os.Args[0])
	if len(name) > 15 {
		t.Skip("test executable exceeds process_name limit")
	}
	c := testkit.Config()
	c.Checks = []model.Check{{ID: "host", Observer: "home", Kind: "host", Host: &model.HostOptions{
		DiskPath: t.TempDir(), ProcessName: name, MinProcesses: 1,
	}}}
	config.Defaults(&c)
	o := NewRunner(c, "home").Run(context.Background(), c, c.Checks[0])
	matches, err := strconv.Atoi(o.Facts["process_matching_count"])
	if o.Outcome != "ok" || err != nil || matches < 1 || o.Facts["memory_total_bytes"] == "" {
		t.Fatal(o, err)
	}
}
