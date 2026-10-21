package outbox

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestDeliveryCopyHasAByteBudget(t *testing.T) {
	q, err := Open(t.TempDir(), "lab", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	recipient := Recipient("a")
	for i := uint64(1); i <= 8; i++ {
		b := makeBatch(i)
		b.Observations[0].Facts["large"] = strings.Repeat("x", 256<<10)
		if err = q.Add(b, []string{recipient}); err != nil {
			t.Fatal(err)
		}
	}
	got := q.PendingBytes(recipient, 100, 512<<10)
	if len(got) != 1 {
		t.Fatal("detached a backlog larger than byte budget", len(got))
	}
	id := got[0].ID
	got[0].Batch.Observations[0].Facts["large"] = "mutated"
	again := q.PendingBytes(recipient, 100, 512<<10)
	if again[0].ID != id || len(again[0].Batch.Observations[0].Facts["large"]) != 256<<10 {
		t.Fatal("byte-limited delivery borrowed mutable queue maps")
	}
	if first := q.PendingBytes(recipient, 100, 1); len(first) != 1 {
		t.Fatal("large first item cannot make progress")
	}
	if all := q.Pending(recipient, 100); len(all) != 8 {
		t.Fatal("diagnostic Pending changed")
	}
}

func TestOutboxRecordLimitAndLazyPayloadRecovery(t *testing.T) {
	dir := t.TempDir()
	q, err := OpenWithOptions(dir, "lab", Options{MaxBytes: 8 << 20, MaxRecords: 3})
	if err != nil {
		t.Fatal(err)
	}
	recipient := Recipient("a")
	for seq := uint64(1); seq <= 3; seq++ {
		b := makeBatch(seq)
		b.Observations[0].Detail = strings.Repeat("x", 128<<10)
		if err = q.Add(b, []string{recipient}); err != nil {
			t.Fatal(err)
		}
	}
	if err = q.Add(makeBatch(4), []string{recipient}); !errors.Is(err, ErrFull) {
		t.Fatal("record limit ignored", err)
	}
	q.Close()
	if opened, err := OpenWithOptions(dir, "lab", Options{MaxBytes: 8 << 20, MaxRecords: 2}); !errors.Is(err, ErrFull) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("restart ignored record limit", err)
	}
	q, err = OpenWithOptions(dir, "lab", Options{MaxBytes: 8 << 20, MaxRecords: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	items, err := q.ReadPending(recipient, 1, 1<<20)
	if err != nil || len(items) != 1 || len(items[0].Batch.Observations[0].Detail) != 128<<10 {
		t.Fatal("lazy recovery lost payload", err)
	}
	// After opening, on-demand reads must still detect missing or corrupt data.
	if err = os.WriteFile(q.file(items[0].ID), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReadPending(recipient, 100, 1<<20); err == nil {
		t.Fatal("corrupt lazy payload accepted")
	}
	if n, _ := q.Stats(); n != 3 {
		t.Fatal("read failure consumed evidence")
	}
}
