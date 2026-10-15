package probe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkLinuxCPURead(b *testing.B) {
	path := filepath.Join(b.TempDir(), "stat")
	raw := "cpu 100 20 30 400 50 10 20 5 80 10\n" + strings.Repeat("cpu0 1 2 3 4 5 6 7 8 9 0\n", 10000)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		b.Fatal(err)
	}
	b.Run("whole_file", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			raw, err := readHostFile(path, 1<<20)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := parseLinuxCPU(string(raw)); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("aggregate_line", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := readLinuxCPU(path); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkLinuxProcessChecks(b *testing.B) {
	root := b.TempDir()
	names := make([]string, 16)
	for i := range names {
		names[i] = fmt.Sprintf("worker-%d", i)
	}
	for i := 1; i <= 512; i++ {
		dir := filepath.Join(root, fmt.Sprint(i))
		if err := os.Mkdir(dir, 0700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(processStat(i, names[i%len(names)], "S", 10)), 0600); err != nil {
			b.Fatal(err)
		}
	}
	for _, shared := range []bool{false, true} {
		label := "per_check"
		if shared {
			label = "shared"
		}
		b.Run(label, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if shared {
					s := newHostRuntime(names)
					if err := linuxProcesses(context.Background(), root, &s); err != nil {
						b.Fatal(err)
					}
				} else {
					for _, name := range names {
						s := newHostRuntime([]string{name})
						if err := linuxProcesses(context.Background(), root, &s); err != nil {
							b.Fatal(err)
						}
					}
				}
			}
		})
	}
}
