package monitor

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"testing"
	"time"
)

func TestLocalStaleReloadReconfirmsRetainedEvidence(t *testing.T) {
	c := testkit.Config()
	c.Checks[0].StaleSeconds = 120
	c.Checks[0].FailureThreshold = 3
	c.Checks[0].RecoveryThreshold = 2
	l := NewLocal(c, "home")
	start := time.Now().UTC()
	rows := []model.Observation{}
	for i, sec := range []int{0, 90, 180, 185} {
		outcome := "fail"
		if i == 3 {
			outcome = "ok"
		}
		v := testkit.Observation(c, "home-http", uint64(i+1), start.Add(time.Duration(sec)*time.Second), outcome)
		rows = append(rows, v)
		l.Record(v)
	}
	at := start.Add(186 * time.Second)
	if got := l.Snapshot(at).Checks[0].Health; got != "degraded" {
		t.Fatalf("bad original fixture: %s", got)
	}
	old := config.Contracts(c)["home-http"]
	c.Checks[0].StaleSeconds = 60
	if config.Contracts(c)["home-http"] != old {
		t.Fatal("stale changed contract")
	}
	l.Reload(c)
	want := engine.Evaluate(c.Checks[0], rows, at)
	got := l.Snapshot(at).Checks[0]
	if want.Health != "up" {
		t.Fatal("bad expected fixture", want)
	}
	if got.Health != want.Health {
		t.Errorf("full history=%s; Local.Reload=%s (%s)", want.Health, got.Health, got.Reason)
	}
}
