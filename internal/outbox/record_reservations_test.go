package outbox

import (
	"errors"
	"testing"
)

func TestRecipientRecordReservationsSurviveRestart(t *testing.T) {
	q, e := OpenWithOptions(t.TempDir(), "lab", Options{MaxBytes: 1 << 20, MaxRecords: 4})
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	offline, healthy := Recipient("https://offline.invalid"), Recipient("https://healthy.invalid")
	for i := uint64(1); i <= 4; i++ {
		if e = q.Add(makeBatch(i), []string{offline, healthy}); e != nil {
			var degraded *DegradedError
			if !errors.As(e, &degraded) {
				t.Fatal(e)
			}
		}
		pending := q.Pending(healthy, 1)
		if len(pending) != 1 {
			t.Fatal(pending)
		}
		if e = q.Ack([]string{pending[0].ID}, healthy); e != nil {
			t.Fatal(e)
		}
	}
	if e = q.Close(); e != nil {
		t.Fatal(e)
	}
	q, e = OpenWithOptions(q.dir, "lab", Options{MaxBytes: 1 << 20, MaxRecords: 4})
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	if len(q.Pending(offline, 0)) != 2 || len(q.Pending(healthy, 0)) != 0 {
		t.Fatal("reservations were not restored")
	}
	if e = q.Add(makeBatch(5), []string{offline, healthy}); e == ErrFull {
		t.Fatalf("offline recipient exhausts global record cap despite healthy recipient having zero backlog; bytes=%d/%d: %v", q.used, q.max, e)
	}
}

func TestRecordBudgetMustCoverRecipients(t *testing.T) {
	q, e := OpenWithOptions(t.TempDir(), "lab", Options{MaxBytes: 1 << 20, MaxRecords: 1})
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	if e = q.Add(makeBatch(1), []string{Recipient("a"), Recipient("b")}); e == nil {
		t.Fatal("impossible reservations admitted")
	}
}
