package outbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestAckBatchSyncsOnceAndPreservesFailedItems(t *testing.T) {
	q := queue(t)
	rid := Recipient("collector")
	for seq := uint64(1); seq <= 4; seq++ {
		if err := q.Add(makeBatch(seq), []string{rid}); err != nil {
			t.Fatal(err)
		}
	}
	items := q.Pending(rid, 0)
	// Fail after one successful deletion using a nonempty directory fixture.
	bad := q.file(items[1].ID)
	if err := os.Rename(bad, bad+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(bad, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "blocked"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	before := ioaccount.DirectorySync.Read().Count
	if err := q.Ack([]string{items[0].ID, items[1].ID, items[2].ID}, rid); err == nil {
		t.Fatal("expected deletion failure")
	}
	if got := ioaccount.DirectorySync.Read().Count - before; got != 1 {
		t.Fatalf("successful prefix synced %d times, want 1", got)
	}
	if got := q.Pending(rid, 0); len(got) != 3 || got[0].ID != items[1].ID {
		t.Fatal("failed/unattempted items lost", got)
	}
	if err := os.RemoveAll(bad); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(bad+".saved", bad); err != nil {
		t.Fatal(err)
	}
	before = ioaccount.DirectorySync.Read().Count
	if err := q.Ack([]string{items[1].ID, items[2].ID, items[3].ID, items[3].ID}, rid); err != nil {
		t.Fatal(err)
	}
	if got := ioaccount.DirectorySync.Read().Count - before; got != 1 {
		t.Fatalf("batch synced %d times", got)
	}
	if len(q.Pending(rid, 0)) != 0 || len(q.pending) != 0 {
		t.Fatal("index retained acknowledged entries")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(q.dir, "lab", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.Pending(rid, 0)) != 0 {
		t.Fatal("acknowledged files recovered")
	}
}

func TestAckSyncFailureRetainsBothErrors(t *testing.T) {
	q := &Queue{dir: filepath.Join(t.TempDir(), "missing")}
	prior := errors.New("delete failed")
	err := q.syncRemoved(true, prior)
	if !errors.Is(err, prior) || !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func BenchmarkIndexedPendingBacklog(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			q := &Queue{items: map[string]Item{}, itemSizes: map[string]int64{}, recipientBytes: map[string]int64{}, pending: map[string]*pendingIndex{}}
			now := time.Now().UTC()
			for i := range n {
				id := fmt.Sprint(i)
				q.account(Item{ID: id, Pending: []string{"recipient"}, Batch: model.Batch{Observations: []model.Observation{{SampledAt: now.Add(time.Duration(i) * time.Second)}}}}, 100)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				q.PendingBytes("recipient", 100, 10000)
			}
		})
	}
}
