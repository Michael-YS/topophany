package outbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectingCollectorRotatesOnlyItsForensicsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(dir, "lab", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { q.Close() }()
	bad, good := Recipient("bad"), Recipient("good")
	for i := uint64(1); i <= 14; i++ {
		b := makeBatch(i)
		b.Observations[0].Facts["large"] = strings.Repeat("x", 300<<10)
		if err := q.Add(b, []string{bad, good}); err != nil {
			t.Fatal("quarantine consumed pending budget", i, err)
		}
		id := q.Pending(bad, 1)[0].ID
		if err := q.Quarantine([]string{id}, bad, "permanent"); err != nil {
			t.Fatal(err)
		}
		if len(q.Pending(good, 1)) != 1 {
			t.Fatal("healthy destination lost its pending copy")
		}
		if err := q.Ack([]string{id}, good); err != nil {
			t.Fatal(err)
		}
		if i == 7 {
			q.Close()
			q, err = Open(dir, "lab", 1<<20)
			if err != nil {
				t.Fatal("quarantine blocked restart", err)
			}
		}
	}
	files, err := os.ReadDir(filepath.Join(dir, "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 || len(files) >= 14 || q.quarantineUsed > 2<<20 {
		t.Fatal("forensic retention failed", len(files), q.quarantineUsed)
	}
	if n, used := q.Stats(); n != 0 || used != q.quarantineUsed {
		t.Fatal("pending accounting leaked", n, used)
	}
}
