package monitor

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/preview"
	"github.com/WhitenWhiten/topophany/internal/view"
)

func richTemplate() Template {
	t := Template{Schema: 1, Title: "Topophany", Rows: []Row{
		{ID: "status", Metric: "summary.verdict", Visual: "status"},
		{ID: "observers", Metric: "observers.verdicts", Visual: "list"},
		{ID: "attention", Metric: "attention.list", Visual: "list"},
		{ID: "applications", Metric: "applications.list", Visual: "list"},
		{ID: "server", Metric: "server.card", Visual: "card", Select: Selector{Node: "app"}},
	}}
	t.Bar.Row = "status"
	return t
}
func TestMetricsAgreeWithView(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	config, err := preview.Preset("en")
	if err != nil {
		t.Fatal(err)
	}
	sim := preview.New(config, now, "en")
	for sc, name := range sim.ScenarioIDs() {
		t.Run(name, func(t *testing.T) {
			sim := preview.New(config, now, "en")
			sim.SetScenario(sc, now.Add(-30*time.Minute))
			snap, err := sim.Snapshot("", now)
			if err != nil {
				return
			} // collector outage has no readable snapshot
			v := view.View{Snapshot: snap, Now: now}
			p := Build(Config{Name: "stream", Source: "preview", Template: richTemplate()}, Input{Snapshot: snap}, now)
			summary := p.Rows[0].Value.(SummaryVerdict)
			if summary.Verdict != v.Verdict() || summary.Counts.Counts != v.Counts() || summary.Configured != v.Counts().Total() {
				t.Fatal(summary, v.Verdict(), v.Counts())
			}
			live, all := v.Live()
			if summary.Observers.Live != live || summary.Observers.All != all {
				t.Fatal(summary)
			}
			observers := p.Rows[1].Value.([]ObserverVerdict)
			for _, o := range observers {
				own, e := sim.Snapshot(o.ID, now)
				if e != nil {
					t.Fatal(e)
				}
				ov := view.View{Snapshot: own, Now: now}
				if o.State != ov.Verdict().State || o.Counts != ov.Counts() {
					t.Fatal(o, ov.Verdict(), ov.Counts())
				}
			}
			rows := v.List(view.Overview, "")
			attention := p.Rows[2].Value.(AttentionList)
			if attention.Total != len(rows) || len(attention.Items) != min(5, len(rows)) {
				t.Fatal(attention, len(rows))
			}
			for i, item := range attention.Items {
				r := rows[i]
				state := view.State(r)
				if r.Kind == "incident" {
					state = view.Pill(r)
				}
				if state == "" {
					state = "up"
				}
				if item.ID != r.ID || item.Title != r.Title || item.Health != r.Health || item.State != strings.ToLower(state) {
					t.Fatal(item, r)
				}
			}
			apps := p.Rows[3].Value.([]ApplicationValue)
			groups := v.ApplicationGroups()
			if len(apps) != min(64, len(groups)) {
				t.Fatal(len(apps), len(groups))
			}
			for i, a := range apps {
				g := groups[i]
				if a.ID != g.ID || a.Health != g.Health || a.Counts != g.Counts || len(a.Services) != min(64, len(g.Rows)) {
					t.Fatal(a, g)
				}
			}
			card := p.Rows[4].Value.(ServerCard)
			vc := v.Fleet().Card("app")
			if card.Where != vc.Where || len(card.Resources) != len(vc.Resources) || len(card.Services) != min(32, len(vc.Services)) {
				t.Fatal(card, vc)
			}
			for i, s := range card.Services {
				c := vc.Services[i]
				if s.ID != c.ID || s.Health != c.Health || s.Affected != c.Affected || s.Monitored != c.Monitored {
					t.Fatal(s, c)
				}
			}
			for _, r := range card.Resources {
				vr, ok := v.HostResource(r.Check)
				if !ok || r.Health != vr.Health || r.Freshness != vr.Freshness {
					t.Fatal(r, vr)
				}
				if vr.Facts == nil && (r.CPU.Percent != nil || r.Memory.Percent != nil || r.Disk.Percent != nil || r.CPU.Logical != nil) {
					t.Fatal("unreadable resources exposed", r)
				}
				if vr.Facts != nil && r.CPU.Percent != nil && fmt.Sprintf("%.1f%%", *r.CPU.Percent) != view.ResourceValues(vr)[0][1] {
					t.Fatal(r, vr)
				}
			}
		})
	}
}
func TestSummarySubsetsAndHealthVocabulary(t *testing.T) {
	now := time.Now().UTC()
	s := model.Snapshot{GeneratedAt: now, Checks: []model.CheckState{{Health: "up"}, {Health: "unknown", Freshness: "paused"}, {Health: "unknown", Freshness: "maintenance"}}}
	v := view.View{Snapshot: s, Now: now}
	got := summary(v)
	if got.Configured != 3 || got.Counts.Unknown != 2 || got.Counts.Paused != 1 || got.Counts.Maintenance != 1 {
		t.Fatal(got)
	}
	for state, want := range map[string]string{"clear": "up", "degraded": "degraded", "incidents": "down", "down": "down", "incomplete": "unknown", "no-evidence": "unknown", "no-checks": "unknown", "cached": "unknown"} {
		if got := verdictHealth(state); got != want {
			t.Fatal(state, got, want)
		}
	}
	c := Config{Template: Template{Rows: []Row{{Metric: "summary.status"}}}}
	if h := Build(c, Input{Snapshot: s}, now).Rows[0].Health; h != "up" {
		t.Fatal(h)
	}
	delivery := deliveryValue(Value{Row: Row{Metric: "delivery.status"}}, []model.DeliveryStatus{{ReportedAt: now, Code: "offline"}}, now)
	if delivery.Health != "degraded" {
		t.Fatal(delivery)
	}
}
func TestRichResourcesWithholdOldNumbers(t *testing.T) {
	now := time.Now().UTC()
	in := hostInput(now)
	in.Snapshot.Topology.Nodes = []model.Node{{ID: "host", Name: "Host"}}
	in.Snapshot.Topology.Observers[0].Node = "host"
	in.Snapshot.Topology.Checks[0].Host = &model.HostOptions{ProcessName: "topo", MaxCPUPercent: 90}
	in.Snapshot.Checks[0].Freshness = "stale"
	v := view.View{Snapshot: in.Snapshot, Now: now}
	r := resource(v, v.HostResources()["host"][0])
	if r.SampledAt == nil || r.CPU.Percent != nil || r.CPU.Limit != nil || r.Process == nil || r.Process.Matching != nil {
		t.Fatal(r)
	}
	c := Config{Source: "preview", Template: richTemplate()}
	p := Build(c, Input{Snapshot: in.Snapshot}, now)
	if p.Rows[4].Value != nil || p.Rows[4].Reason != "entity_missing" {
		t.Fatal(p.Rows[4])
	}
	v.Cached = true
	for _, a := range applications(v) {
		if a.Health != "unknown" {
			t.Fatal(a)
		}
	}
}
func TestMetricVisualAndSelectorValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Template)
		source string
		bad    bool
	}{
		{"valid", func(*Template) {}, "preview", false},
		{"worker", func(*Template) {}, "worker", true},
		{"wrong visual", func(t *Template) { t.Rows[0].Visual = "text" }, "client", true},
		{"node missing", func(t *Template) { t.Rows[4].Select.Node = "" }, "client", true},
		{"node unexpected", func(t *Template) { t.Rows[0].Select.Node = "app" }, "client", true},
		{"limit zero", func(t *Template) { n := 0; t.Rows[2].Limit = &n }, "client", true},
		{"limit high", func(t *Template) { n := 21; t.Rows[2].Limit = &n }, "client", true},
		{"limit wrong metric", func(t *Template) { n := 5; t.Rows[0].Limit = &n }, "client", true},
		{"limit twenty", func(t *Template) { n := 20; t.Rows[2].Limit = &n }, "client", false},
		{"duplicate", func(t *Template) { t.Rows[1].ID = "status" }, "client", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl := richTemplate()
			tc.change(&tpl)
			err := tpl.Validate(tc.source)
			if (err != nil) != tc.bad {
				t.Fatal(err)
			}
			if err != nil && !strings.Contains(err.Error(), "row ") {
				t.Fatal("validation did not name row", err)
			}
		})
	}
}

func TestMetricCollectionBoundsAndTopologyOrder(t *testing.T) {
	now := time.Now().UTC()
	snap := model.Snapshot{GeneratedAt: now, Topology: model.Config{Nodes: []model.Node{{ID: "host"}}}}
	// Reverse the labels to make topology order differ from alphabetical order.
	for i := 0; i < 65; i++ {
		id := fmt.Sprintf("app-%02d", i)
		snap.Topology.Applications = append(snap.Topology.Applications, model.Application{ID: id, Name: fmt.Sprintf("App %02d", 65-i)})
		members := 1
		if i == 0 {
			members = 65
		}
		for j := 0; j < members; j++ {
			service := fmt.Sprintf("%s-svc-%02d", id, j)
			snap.Topology.Services = append(snap.Topology.Services, model.Service{ID: service, Name: service, Node: "host", Application: id})
			snap.Services = append(snap.Services, model.EntityState{ID: service, Health: "up", Checks: []string{"check"}})
		}
	}
	slices.Reverse(snap.Services)
	apps := applications(view.View{Snapshot: snap, Now: now})
	if len(apps) != 64 || apps[0].ID != "app-00" || len(apps[0].Services) != 64 || apps[63].ID != "app-63" || apps[0].Services[0].ID != "app-00-svc-00" {
		t.Fatal(len(apps), apps[0], apps[len(apps)-1])
	}
	v := view.View{Snapshot: snap, Now: now}
	card, _, _, ok := server(v, v, "host")
	if !ok || len(card.Services) != 32 {
		t.Fatal(card)
	}
}
