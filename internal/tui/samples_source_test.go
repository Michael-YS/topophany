package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestSamplerDiscardsHistoryAfterSnapshotSwitch(t *testing.T) {
	for _, change := range []string{"source", "revision", "contract"} {
		t.Run(change, func(t *testing.T) {
			m := previewModel(t)
			m.Server = "collector-a"
			m.setTab(tabChecks)
			id := selectFirstEntry(t, m).ID
			started, finish := make(chan struct{}), make(chan struct{})
			s := sampler{results: make(chan fetchedSamples, 1), readAt: func(_ context.Context, v client.HistoryView, _ int) ([]model.Observation, error) {
				close(started)
				<-finish
				return []model.Observation{{Check: id, Contract: v.Contract, SampledAt: time.Now()}}, nil
			}}
			s.request(context.Background(), m, false)
			<-started
			switch change {
			case "source":
				m.Server = "collector-b"
			case "revision":
				m.Snapshot.Revision = strings.Repeat("a", 64)
			case "contract":
				for i := range m.Snapshot.Checks {
					if m.Snapshot.Checks[i].ID == id {
						m.Snapshot.Checks[i].Contract = strings.Repeat("b", 64)
					}
				}
			}
			close(finish)
			got := <-s.results
			s.keep(m, got)
			if len(m.samples.entries) != 0 || s.busy {
				t.Fatal("history from a superseded snapshot was retained")
			}
			got.trend = true
			s.keep(m, got)
			if len(m.trends) != 0 {
				t.Fatal("superseded history reached host charts")
			}
		})
	}
}

func TestSamplerClearsCachedHistoryOnSourceOrRevisionChange(t *testing.T) {
	for _, change := range []string{"source", "revision"} {
		t.Run(change, func(t *testing.T) {
			m := previewModel(t)
			m.Server = "collector-a"
			m.samples = samples{check: "old", entries: []model.Observation{{Outcome: "ok"}}}
			m.trends = map[string]*trend{"old": {samples: m.samples.entries}}
			m.absorbed = m.Snapshot.GeneratedAt
			s := sampler{busy: true, server: m.Server, revision: m.Snapshot.Revision, readAt: func(context.Context, client.HistoryView, int) ([]model.Observation, error) { return nil, nil }}
			if change == "source" {
				m.Server = "collector-b"
			} else {
				m.Snapshot.Revision = strings.Repeat("a", 64)
			}
			s.request(context.Background(), m, true)
			if len(m.samples.entries) != 0 || len(m.trends) != 0 || !m.absorbed.IsZero() {
				t.Fatal("cached samples or host trends crossed the snapshot source boundary")
			}
		})
	}
}
