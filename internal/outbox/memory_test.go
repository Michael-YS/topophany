package outbox

import (
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
