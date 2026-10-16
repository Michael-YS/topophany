package monitor

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func testConfig() Config {
	c := Config{Schema: 1, Name: "desktop", Enabled: true, Source: "collector", Interval: "5s", Output: filepath.Join(string(filepath.Separator), "tmp", "metrics.json"), Template: Template{Schema: 1, Title: "Topophany", Rows: []Row{{ID: "cpu", Metric: "host.cpu.used_percent", Visual: "bar", Format: "percent", Select: Selector{Check: "host"}}}}}
	c.Template.Bar.Row = "cpu"
	return c
}
func hostInput(now time.Time) Input {
	return Input{Snapshot: model.Snapshot{GeneratedAt: now, Project: "test", Topology: model.Config{Checks: []model.Check{{ID: "host", Kind: "host", Observer: "local", StaleSeconds: 10}}, Observers: []model.Observer{{ID: "local"}}}, Checks: []model.CheckState{{ID: "host", Observer: "local", Health: "up", Freshness: "fresh", Last: &model.Observation{SampledAt: now, Outcome: "ok", Evidence: "reported", Facts: map[string]string{"cpu_used_percent": "42"}}}}}}
}
func TestEvidenceExpiresWithoutNewSample(t *testing.T) {
	now := time.Now()
	c := testConfig()
	in := hostInput(now)
	p := Build(c, in, now)
	if p.Rows[0].Value != float64(42) {
		t.Fatal(p)
	}
	p = Build(c, in, now.Add(11*time.Second))
	if p.Rows[0].Value != nil || p.Rows[0].Freshness != "stale" {
		t.Fatal(p)
	}
	if !p.Rows[0].SampledAt.Equal(now) {
		t.Fatal("rewrote evidence timestamp")
	}
}
func TestAmbiguityInvalidValuesAndMissingSource(t *testing.T) {
	now := time.Now()
	c := testConfig()
	c.Template.Rows[0].Select.Check = ""
	in := hostInput(now)
	in.Snapshot.Checks = append(in.Snapshot.Checks, in.Snapshot.Checks[0])
	if v := Build(c, in, now).Rows[0]; v.Reason != "selector_ambiguous" {
		t.Fatal(v)
	}
	in = hostInput(now)
	in.Snapshot.Checks[0].Last.Facts["cpu_used_percent"] = "NaN"
	if v := Build(c, in, now).Rows[0]; v.Value != nil {
		t.Fatal(v)
	}
	in.Unavailable = true
	if v := Build(c, in, now).Rows[0]; v.Reason != "source_unavailable" {
		t.Fatal(v)
	}
}
func TestTemplateCapabilitiesAndAtomicConfig(t *testing.T) {
	c := testConfig()
	c.Output = filepath.Join(t.TempDir(), "metrics.json")
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	saved, e := Save(path, c)
	if e != nil {
		t.Fatal(e)
	}
	c.Template.Bar.Row = "missing"
	if _, e = Save(path, c); e == nil {
		t.Fatal("accepted invalid reference")
	}
	loaded, e := Load(path)
	if e != nil || loaded.Revision != saved.Revision || loaded.Template.Bar.Row != "cpu" {
		t.Fatal(loaded, e)
	}
	c = testConfig()
	c.Template.Rows[0].Metric = "summary.status"
	c.Template.Rows[0].Visual = "text"
	c.Template.Rows[0].Select = Selector{}
	c.Template.Rows[0].Format = ""
	if e = c.Template.Validate("worker"); e == nil {
		t.Fatal("worker accepted global metric")
	}
}
