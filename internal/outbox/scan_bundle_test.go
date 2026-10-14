package outbox

import (
	"context"
	"errors"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUnacknowledgedDestinationCannotStopHealthyDelivery(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(dir, "lab", 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { q.Close() }()
	bad, good := Recipient("bad"), Recipient("good")
	degraded := 0
	for i := uint64(1); i <= 30; i++ {
		err = q.Add(makeBatch(i), []string{bad, good})
		var loss *DegradedError
		if errors.As(err, &loss) {
			degraded++
			if len(loss.Recipients) != 1 || loss.Recipients[0] != bad {
				t.Fatal("healthy quota lost", loss)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		healthy := q.Pending(good, 100)
		if len(healthy) != 1 || healthy[0].Batch.Observations[0].Sequence != i {
			t.Fatal("healthy evidence stopped", i)
		}
		if err := q.Ack([]string{healthy[0].ID}, good); err != nil {
			t.Fatal(err)
		}
		if i == 15 {
			q.Close()
			q, err = Open(dir, "lab", 8000)
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, used := q.Stats(); used > 8000 {
			t.Fatal("global budget exceeded")
		}
	}
	if degraded < 20 || len(q.Pending(bad, 100)) == 0 {
		t.Fatal("test failed to exhaust bad recipient", degraded)
	}
}

func TestInvalidACKCollectorIsolationEndToEnd(t *testing.T) {
	c := testkit.Config()
	good, st := collector(t, c, nil)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		w.Write([]byte(`{"project":"wrong","revision":"old","accepted":1}`))
	}))
	defer bad.Close()
	d, err := NewDelivery(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: good.URL, Token: workerToken}, {URL: bad.URL, Token: workerToken}}}, config.Revision(c))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := time.Now()
	d.Now = func() time.Time { return now }
	dir := t.TempDir()
	q, err := Open(dir, c.Project, 8000)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { q.Close() }()
	losses := 0
	for i := uint64(1); i <= 15; i++ {
		err = q.Add(makeBatch(i), d.Recipients())
		var degraded *DegradedError
		if errors.As(err, &degraded) {
			losses++
		} else if err != nil {
			t.Fatal(err)
		}
		results := d.Tick(context.Background(), q, nil)
		if results[0].Accepted != 1 {
			t.Fatal("healthy delivery stopped", i, results)
		}
		if rows, _ := st.View(); len(rows) != int(i) {
			t.Fatal("healthy collector lost evidence", len(rows), i)
		}
		if i == 8 {
			q.Close()
			q, err = Open(dir, c.Project, 8000)
			if err != nil {
				t.Fatal(err)
			}
		}
		now = now.Add(time.Minute)
	}
	if losses == 0 || len(q.Pending(Recipient(bad.URL), 100)) == 0 {
		t.Fatal("bad collector never exhausted its reservation")
	}
}
