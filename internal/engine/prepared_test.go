package engine

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestPreparedEvidenceRemainsTimeSensitiveAndOwnsInputs(t *testing.T) {
	c := testkit.Config()
	start := time.Now().UTC()
	rows := []model.Observation{
		testkit.Observation(c, "home-http", 1, start.Add(-time.Second), "fail"),
		testkit.Observation(c, "home-http", 2, start.Add(10*time.Second), "ok"),
	}
	beats := map[string]model.Heartbeat{"home": {Observer: "home", Project: c.Project, Revision: config.Revision(c), SampledAt: start, Delivery: []model.DeliveryStatus{{Collector: "0123456789abcdef"}}}}
	p := NewPlan(c)
	e := p.Prepare(rows, nil, beats)
	for _, offset := range []time.Duration{0, 5 * time.Second, 10 * time.Second, 24 * time.Hour, -time.Hour, 0} {
		now := start.Add(offset)
		got := e.Build("test", "", now)
		for _, state := range got.Checks {
			var check model.Check
			for _, x := range c.Checks {
				if x.ID == state.ID {
					check = x
				}
			}
			var history []model.Observation
			for _, v := range rows {
				if v.Check == state.ID {
					history = append(history, v)
				}
			}
			want := Evaluate(check, history, now)
			want.Contract = config.PublicContracts(c)[state.ID]
			if want.Last != nil {
				want.Last.Contract, want.Last.Revision = want.Contract, ""
			}
			if !reflect.DeepEqual(state, want) {
				t.Fatalf("at %s: got %+v want %+v", offset, state, want)
			}
		}
		for _, observer := range got.Observers {
			if observer.ID != "home" {
				continue
			}
			want := start
			if offset >= 5*time.Second {
				want = start.Add(10 * time.Second)
			}
			if offset < -5*time.Second {
				want = time.Time{}
			}
			if !observer.LastSeen.Equal(want) {
				t.Fatalf("at %s last seen %s want %s", offset, observer.LastSeen, want)
			}
		}
	}
	baseline := e.Build("test", "", start)
	encoded, _ := json.Marshal(baseline)
	rows[1].Facts["response_proof"] = "input changed"
	beats["home"].Delivery[0].Code = "input changed"
	baseline.Topology.Nodes[0].Name = "output changed"
	baseline.Checks[0].Last.Facts["response_proof"] = "output changed"
	baseline.Observers[0].Delivery[0].Code = "output changed"
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			got, _ := json.Marshal(e.Build("test", "", start))
			if string(got) != string(encoded) {
				t.Error("prepared evidence aliases mutable data")
			}
		})
	}
	wg.Wait()
}
