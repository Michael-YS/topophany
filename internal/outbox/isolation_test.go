package outbox

import (
	"context"
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFanoutRejectionKeepsValidMembersAcrossRestart(t *testing.T) {
	old := testkit.Config()
	// Both observations are valid under the original topology. This uses the
	// same multi-observation item shape as external source fan-out.
	first := testkit.Observation(old, "home-http", 1, time.Now().Add(-time.Second), "ok")
	second := testkit.Observation(old, "home-ssh", 2, time.Now().Add(-time.Second), "ok")
	next := config.Clone(old)
	next.Checks[1].TimeoutSeconds++
	srv, st := collector(t, next, nil)
	d, e := NewDelivery(client.Profile{Project: old.Project, Servers: []client.Endpoint{{URL: srv.URL, Token: workerToken}}}, config.Revision(old))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	q := queue(t)
	if e = q.Add(model.Batch{Schema: model.BatchSchema, Project: old.Project, Revision: config.Revision(old), Observations: []model.Observation{first, second}}, d.Recipients()); e != nil {
		t.Fatal(e)
	}
	d.Tick(context.Background(), q, nil)
	firstResult := d.Tick(context.Background(), q, nil)
	if firstResult[0].Accepted != 1 {
		t.Fatal(firstResult)
	}
	// Lose the in-memory isolation cursor, as on a process restart.
	d.Close()
	if e = q.Close(); e != nil {
		t.Fatal(e)
	}
	q, e = Open(q.dir, "lab", 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	d, e = NewDelivery(client.Profile{Project: old.Project, Servers: []client.Endpoint{{URL: srv.URL, Token: workerToken}}}, config.Revision(old))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	quarantined := 0
	for range 4 {
		result := d.Tick(context.Background(), q, nil)
		quarantined += result[0].Quarantined
	}
	rows, _ := st.View()
	if len(rows) != 1 || rows[0].ID != first.ID || quarantined != 1 {
		t.Fatalf("valid member lost or duplicated: rows=%+v quarantined=%d", rows, quarantined)
	}
	if n, _ := q.Stats(); n != 0 {
		t.Fatal("parent remains pending", n)
	}
	files, e := os.ReadDir(filepath.Join(q.dir, "quarantine"))
	if e != nil || len(files) != 1 {
		t.Fatal(files, e)
	}
	raw, e := os.ReadFile(filepath.Join(q.dir, "quarantine", files[0].Name()))
	if e != nil {
		t.Fatal(e)
	}
	var evidence struct {
		Item Item `json:"item"`
	}
	if e = json.Unmarshal(raw, &evidence); e != nil {
		t.Fatal(e)
	}
	if len(evidence.Item.Batch.Observations) != 1 || evidence.Item.Batch.Observations[0].ID != second.ID || !validItem(evidence.Item) {
		t.Fatal("invalid quarantine evidence", string(raw))
	}
}
