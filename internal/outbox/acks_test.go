package outbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/ioaccount"
)

func TestPartialAckBatchOneSyncAndCrashReplay(t *testing.T) {
	q := queue(t)
	a, b := Recipient("a"), Recipient("b")
	ids := []string{}
	for i := uint64(1); i <= 100; i++ {
		if err := q.Add(makeBatch(i), []string{a, b}); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range q.Pending(a, 0) {
		ids = append(ids, v.ID)
	}
	original, err := os.ReadFile(q.file(ids[0]))
	if err != nil {
		t.Fatal(err)
	}
	before := ioaccount.FileSync.Read().Count
	if err = q.Ack(ids, a); err != nil {
		t.Fatal(err)
	}
	if got := ioaccount.FileSync.Read().Count - before; got != 1 {
		t.Fatalf("partial batch used %d fsyncs", got)
	}
	after, err := os.ReadFile(q.file(ids[0]))
	if err != nil || string(after) != string(original) {
		t.Fatal("partial ACK rewrote payload", err)
	}
	// No clean shutdown flush is needed for ACK recovery. A torn trailing write
	// must not hide the preceding committed batch or poison later appends.
	if _, err = q.ackFile.Write([]byte(`{"project":`)); err != nil {
		t.Fatal(err)
	}
	dir := q.dir
	q.Close()
	q, err = Open(dir, "lab", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if len(q.Pending(a, 0)) != 0 || len(q.Pending(b, 0)) != 100 {
		t.Fatal("lost independent partial ACKs")
	}
	if err = q.compactAcks(); err != nil {
		t.Fatal(err)
	}
	q.Close()
	q, err = Open(dir, "lab", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if len(q.Pending(a, 0)) != 0 || len(q.Pending(b, 0)) != 100 {
		t.Fatal("compaction lost pending recipients")
	}
	if err = q.Ack(ids, b); err != nil {
		t.Fatal(err)
	}
	// A formerly acknowledged identity can intentionally be queued again.
	batch := makeBatch(200)
	if err = q.Add(batch, []string{a, b}); err != nil {
		t.Fatal(err)
	}
	id := q.Pending(a, 1)[0].ID
	if err = q.Ack([]string{id}, a); err != nil {
		t.Fatal(err)
	}
	if err = q.Ack([]string{id}, b); err != nil {
		t.Fatal(err)
	}
	if err = q.Add(batch, []string{a, b}); err != nil {
		t.Fatal(err)
	}
	q.Close()
	q, err = Open(dir, "lab", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if len(q.Pending(a, 0)) != 1 || len(q.Pending(b, 0)) != 1 {
		t.Fatal("old ACK consumed a new queue entry")
	}
}

func TestAckFailureAndCorruptionFailClosed(t *testing.T) {
	q := queue(t)
	a, b := Recipient("a"), Recipient("b")
	if err := q.Add(makeBatch(1), []string{a, b}); err != nil {
		t.Fatal(err)
	}
	id := q.Pending(a, 1)[0].ID
	q.ackFile.Close()
	if err := q.Ack([]string{id}, a); err == nil {
		t.Fatal("failed ACK accepted")
	}
	if len(q.Pending(a, 0)) != 1 || len(q.Pending(b, 0)) != 1 {
		t.Fatal("failed ACK lost an item")
	}
	if err := q.Add(makeBatch(2), []string{a}); err == nil {
		t.Fatal("uncertain journal continued writing")
	}
	dir := q.dir
	q.Close()
	if err := os.WriteFile(filepath.Join(dir, "acks.jsonl"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(dir, "lab", 1<<20); err == nil {
		opened.Close()
		t.Fatal("corrupt ACK journal accepted")
	}
}
