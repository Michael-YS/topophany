package app

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// A collector says what its store holds when it starts, and warns once when
// an observer's receipts are set to fill its share.
func TestCollectorWarnsBeforeAnObserverFillsItsShare(t *testing.T) {
	c := testkit.Config()
	st, e := store.Open(t.TempDir(), store.Options{Observers: []string{"home", "cloud"}, MaxReceipts: 400, Retention: time.Hour})
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	now := time.Now().UTC()
	vs := []model.Observation{}
	for i := 0; i < 150; i++ {
		vs = append(vs, testkit.Observation(c, "home-http", uint64(i+1), now, "ok"))
	}
	if e := st.Put(model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: vs}); e != nil {
		t.Fatal(e)
	}
	log := &lockedWriter{}
	a := &App{In: strings.NewReader(""), Out: io.Discard, Err: log}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.watchStorage(ctx, st, 10*time.Millisecond) }()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(log.String(), "fills its share") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	out := log.String()
	if !strings.HasPrefix(out, "storage: 150 of 400 receipts") || strings.Count(out, "storage: observer home fills its share in about 20m0s") != 1 || strings.Contains(out, "observer cloud") {
		t.Fatal(out)
	}
}
