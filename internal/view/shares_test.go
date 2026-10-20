package view

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// A first-byte line names each cache class the rollup holds, estimates its
// p50 and p95, and leaves out a class with no passing samples.
func TestFirstByteLine(t *testing.T) {
	if got := FirstByteLine(nil); got != "" {
		t.Fatalf("no first byte reads %q", got)
	}
	line := FirstByteLine(&model.FirstByteLatency{
		Cached: &model.Latency{Samples: 2, P50: 40, P95: 90, Max: 120},
		Origin: &model.Latency{Samples: 1, P50: 600, P95: 600, Max: 600},
	})
	for _, want := range []string{"cached p50 40 ms", "p95 90 ms", "origin p50 600 ms", "p95 600 ms"} {
		if !strings.Contains(line, want) {
			t.Fatalf("first-byte line lacks %q: %q", want, line)
		}
	}
	if got := FirstByteLine(&model.FirstByteLatency{Origin: &model.Latency{Samples: 1, P50: 600}}); strings.Contains(got, "cached") {
		t.Fatalf("an absent class was named: %q", got)
	}
}

// Colo shares are ordered largest first, count other in the denominator but
// never list it, keep at most the asked-for number, and have nothing to say
// when no named colo was counted.
func TestPopShares(t *testing.T) {
	pops := map[string]int64{"LAX": 92, "HKG": 7, "other": 1}
	if got := PopShares(pops, 4); !slicesEqual(got, []string{"LAX 92%", "HKG 7%"}) {
		t.Fatalf("shares %v", got)
	}
	if got := PopsLine(pops, 1); got != "LAX 92%" {
		t.Fatalf("bounded line %q", got)
	}
	if got := PopShares(map[string]int64{"other": 5}, 4); got != nil {
		t.Fatalf("other alone was listed: %v", got)
	}
	if got := PopShares(nil, 4); got != nil {
		t.Fatalf("no counts produced %v", got)
	}
	if got := PopShares(map[string]int64{"LAX": 1, "hkg": 2, "OTHER": 1}, 4); !slicesEqual(got, []string{"LAX 25%"}) {
		// The rollup never writes a lowercase code, but a malformed one is not
		// a colo and must not be shown as if it were.
		t.Fatalf("malformed codes were listed: %v", got)
	}
	if got := PopShares(map[string]int64{"LAX": 1, "other": 1000000}, 4); !slicesEqual(got, []string{"LAX <1%"}) {
		t.Fatalf("a sliver reads %v", got)
	}
}

func slicesEqual(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

// RollupWindow finds the day a card or row asks for, and invents no window
// the collector did not report.
func TestRollupWindow(t *testing.T) {
	list := []model.Availability{
		{Window: "24h", Rollup: model.Rollup{Pops: map[string]int64{"LAX": 92}}},
		{Window: "7d"},
	}
	if got := RollupWindow(list, "24h"); got == nil || got.Pops["LAX"] != 92 {
		t.Fatalf("RollupWindow missed the day: %+v", got)
	}
	if got := RollupWindow(list, "30d"); got != nil {
		t.Fatalf("RollupWindow invented a window: %+v", got)
	}
	if got := RollupWindow(nil, "24h"); got != nil {
		t.Fatalf("RollupWindow invented a window from nothing: %+v", got)
	}
}

// An edge card orders the colos by their 24h share and shows the share.
// With no rollups it falls back to the fresh response facts as bare codes,
// so a new collector still says something.
func TestEdgeCardOrdersPopsByDayShare(t *testing.T) {
	c := testkit.Config()
	c.Nodes = append(c.Nodes, model.Node{ID: "cf", Name: "Cloudflare", Kind: "edge", Provider: "cloudflare"})
	for i := range c.Endpoints {
		if c.Endpoints[i].ID == "dmit-http" {
			c.Endpoints[i].Via = []string{"cf"}
		}
	}
	now := time.Date(2026, 10, 19, 18, 0, 0, 0, time.UTC)
	x, _ := config.Check(c, "home-http")
	o := testkit.Observation(c, x.ID, 1, now.Add(-time.Second), "ok")
	o.Facts["edge_provider"], o.Facts["edge_pop"] = "cloudflare", "EWR"
	snap := engine.Build(c, "primary", "", []model.Observation{o}, nil, now)
	for i := range snap.Checks {
		if snap.Checks[i].ID == x.ID {
			snap.Checks[i].Availability = []model.Availability{{Window: "24h", Rollup: model.Rollup{Pops: map[string]int64{"LAX": 90, "HKG": 10}}}}
		}
	}
	card := (View{Snapshot: snap, Now: now}).Fleet().Card("cf")
	if !slicesEqual(card.Pops, []string{"LAX 90%", "HKG 10%"}) || !card.PopsFromRollups {
		t.Fatalf("day shares %v from rollups %v, want the rollup order", card.Pops, card.PopsFromRollups)
	}
	// No availability: the fresh fact is the fallback and is not a share.
	snap.Checks[0].Availability = nil
	card = (View{Snapshot: snap, Now: now}).Fleet().Card("cf")
	if !slicesEqual(card.Pops, []string{"EWR"}) || card.PopsFromRollups {
		t.Fatalf("fallback %v from rollups %v, want the fresh colo", card.Pops, card.PopsFromRollups)
	}
}

// Many checks with many distinct colos cannot make a card list them all:
// the card keeps the six largest shares and says they came from rollups.
func TestEdgeCardCapsManyColoChecks(t *testing.T) {
	c := testkit.Config()
	c.Nodes = append(c.Nodes, model.Node{ID: "cf", Name: "Cloudflare", Kind: "edge", Provider: "cloudflare"})
	for i := range c.Endpoints {
		if c.Endpoints[i].ID == "dmit-http" {
			c.Endpoints[i].Via = []string{"cf"}
		}
	}
	base := c.Checks[0]
	for i := 0; i < 20; i++ {
		x := base
		x.ID, x.Observer, x.Endpoint = fmt.Sprintf("edge-%02d", i), "home", "dmit-http"
		c.Checks = append(c.Checks, x)
	}
	now := time.Date(2026, 10, 19, 18, 0, 0, 0, time.UTC)
	snap := engine.Build(c, "primary", "", nil, nil, now)
	total := 0
	for i := range snap.Checks {
		pops := map[string]int64{}
		for j := 0; j < 16; j++ {
			pops[testColo(total)] = int64(total + 1)
			total++
		}
		snap.Checks[i].Availability = []model.Availability{{Window: "24h", Rollup: model.Rollup{Pops: pops}}}
	}
	card := (View{Snapshot: snap, Now: now}).Fleet().Card("cf")
	if len(card.Pops) != cardPops || !card.PopsFromRollups {
		t.Fatalf("card lists %d colos from rollups=%v, want %d", len(card.Pops), card.PopsFromRollups, cardPops)
	}
	// Counts grew with the code order, so the largest code's share leads,
	// lower-bounded and never overstating its share.
	largest := testColo(total - 1)
	if !strings.HasPrefix(card.Pops[0], largest+" ") || !strings.HasSuffix(card.Pops[0], "%") {
		t.Fatalf("the largest share did not lead: %v", card.Pops)
	}
	for _, p := range card.Pops {
		if !strings.Contains(p, "%") {
			t.Fatalf("a listed colo is not a share: %v", card.Pops)
		}
	}
}

// testColo encodes n as three uppercase letters, for fixtures that need
// more distinct codes than a hand-written list carries.
func testColo(n int) string {
	return string([]byte{byte('A' + n/676%26), byte('A' + n/26%26), byte('A' + n%26)})
}
