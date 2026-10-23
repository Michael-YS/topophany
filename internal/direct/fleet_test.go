package direct

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/report"
	"github.com/WhitenWhiten/topophany/internal/source"
	"testing"
	"time"
)

func fleetSession(t testing.TB, objects, seen int) (*Session, model.Config, time.Time) {
	t.Helper()
	p := Profile{Profile: "audit", Project: "direct-audit", Revision: "revision", Dataset: "dataset"}
	for i := 0; i < objects; i++ {
		if i%20 == 0 {
			id := fmt.Sprintf("s%d", i/20)
			p.Sources = append(p.Sources, Entry{Source: SourceConfig{ID: id, Name: id, Adapter: "beszel", URL: "https://example.invalid", Interval: 60, Timeout: 8, Stale: 180, MaxAge: 180}, Credential: "synthetic", Subject: "users:uuuuuuuuuuuuuuu", AuthMode: "token"})
		}
		e := &p.Sources[len(p.Sources)-1]
		obj := fmt.Sprintf("%015d", i)
		e.Selected = append(e.Selected, obj)
		e.Nodes = append(e.Nodes, Node{ID: e.Source.ID + "." + obj, ObjectID: obj, Name: obj})
	}
	c, err := p.Config()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	s := &Session{ctx: context.Background(), profile: p, config: c, opts: Options{Now: func() time.Time { return now }, MaxStateBytes: MaxStateBytes, MaxSourceStateBytes: MaxSourceStateBytes}, external: model.NewExternalState(), loops: map[string]*sourceLoop{}, plan: engine.NewPlan(c)}
	for _, src := range c.Sources {
		s.loops[src.ID] = &sourceLoop{generation: "audit"}
		s.external.Sources[src.ID] = report.MergeSource(model.SourceState{}, src.ID, now.Add(-time.Minute), true, "", 0)
	}
	for _, x := range c.Checks {
		state := model.BindingState{}
		for j := 0; j < seen; j++ {
			at := now.Add(time.Duration(j-seen) * time.Minute)
			r := resourceReading(at, fmt.Sprintf("komari-%064d", j))
			r.Object = x.External.Object
			r.Report.Object = r.Object
			state, _ = report.MergeBinding(x.ID, config.Contract(c, x), *x.External, state, r.Report, at)
		}
		s.external.Bindings[x.ID] = state
	}
	s.prepared = s.plan.PrepareExternal(nil, nil, nil, s.external)
	return s, c, now
}

func TestFullFleetRetainsCompleteDeduplicationWindow(t *testing.T) {
	s, c, now := fleetSession(t, 256, 256)
	b, _ := json.Marshal(s.external)
	t.Logf("initial accepted-state bytes=%d, limit=%d", len(b), MaxStateBytes)
	if len(b) > MaxStateBytes {
		t.Fatal("seed exceeds limit")
	}
	for round := 256; round <= 258; round++ {
		for _, src := range c.Sources {
			var readings []source.Reading
			for _, x := range c.Checks {
				if x.External.Source == src.ID {
					r := resourceReading(now.Add(-time.Second), fmt.Sprintf("komari-%064d", round))
					r.Object = x.External.Object
					r.Report.Object = r.Object
					// Report identities must advance in upstream time as well.
					r.Report.ObservedAt = now.Add(time.Duration(round-256) * time.Millisecond)
					readings = append(readings, r)
				}
			}
			if e := s.Record("audit", src.ID, now, now, readings, nil); e != nil {
				b, _ = json.Marshal(s.external)
				t.Fatalf("valid 256-object profile stops at unique sample %d (source %s): %v, retained bytes=%d", round+1, src.ID, e, len(b))
			}
		}
	}
}
