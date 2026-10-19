package stream

import (
	"context"
	"github.com/WhitenWhiten/topophany/internal/monitor"
	"slices"
	"testing"
	"time"
)

func TestStreamCallbackOwnsEveryFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := request()
	r.Scenario = 2
	n := 1
	r.Template.Rows = append(r.Template.Rows,
		monitor.Row{ID: "attention", Metric: "attention.list", Visual: "list", Limit: &n},
		monitor.Row{ID: "server", Metric: "server.card", Visual: "card", Select: monitor.Selector{Node: "app"}},
	)
	lines := make(chan Line, 1)
	lines <- line(r)
	count := 0
	open := func(ctx context.Context, r Request) (Source, *Error) {
		s, e := OpenSource(ctx, r)
		fetch := s.Fetch
		nextUp := false
		s.Fetch = func(ctx context.Context) (monitor.Input, error) {
			in, err := fetch(ctx)
			// Ownership needs distinct published frames. A stable preview may
			// otherwise be coalesced until the ten-second lease renewal.
			in.Snapshot.Checks = slices.Clone(in.Snapshot.Checks)
			if len(in.Snapshot.Checks) > 0 {
				in.Snapshot.Checks[0].Health = map[bool]string{true: "up", false: "down"}[nextUp]
				nextUp = !nextUp
			}
			return in, err
		}
		return s, e
	}
	err := Run(ctx, lines, open, func(p monitor.Panel) error {
		if p.Lifecycle != "running" {
			return nil
		}
		count++
		if *p.Rows[1].Limit != 1 || p.Rows[0].ID != "status" {
			t.Error("callback changed request", p.Rows)
		}
		attention := p.Rows[1].Value.(monitor.AttentionList)
		if len(attention.Items) != 1 || attention.Items[0].Route[0] == "mutated" {
			t.Error("callback changed later attention", attention)
		}
		card := p.Rows[2].Value.(monitor.ServerCard)
		if card.Resources[0].CPU.Percent == nil || *card.Resources[0].CPU.Percent == 999 || card.Observes[0].LastSeen.IsZero() || card.Observes[0].ValidUntil.IsZero() {
			t.Error("callback changed later card", card)
		}
		// These mutations are supported by the owned snapshot API. They must not
		// affect configuration, source inputs, cadence state or another callback.
		*p.Rows[1].Limit = 20
		p.Rows[0].ID = "mutated"
		attention.Items[0].Route[0] = "mutated"
		*card.Resources[0].CPU.Percent = 999
		*card.Observes[0].LastSeen = time.Time{}
		*card.Observes[0].ValidUntil = time.Time{}
		if count == 3 {
			cancel()
		}
		return nil
	})
	if err != nil || count != 3 {
		t.Fatal(err, count)
	}
}
