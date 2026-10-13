package engine

import (
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestPathDependencyImpactPropagatesTransitively(t *testing.T) {
	c := testkit.Config()
	c.Paths = append(c.Paths, model.Path{ID: "middle", Observer: "home", DependsOn: []string{"direct"}}, model.Path{ID: "outer", Observer: "home", DependsOn: []string{"middle"}})
	now := time.Now()
	rows := []model.Observation{}
	for i := 1; i <= 3; i++ {
		rows = append(rows, testkit.Observation(c, "home-http", uint64(i), now.Add(time.Duration(i-4)*time.Second), "fail"))
	}
	// The other direct-path check is unknown, so make the failing check the
	// only independently measured evidence for this dependency.
	c.Checks = append(c.Checks[:1], c.Checks[2:]...)
	for i := range rows {
		rows[i] = testkit.Observation(c, "home-http", uint64(i+1), rows[i].SampledAt, "fail")
	}
	s := Build(c, "test", "home", rows, nil, now)
	for _, p := range s.Paths {
		if (p.ID == "middle" || p.ID == "outer") && (!p.Affected || p.Health == "down") {
			t.Fatal("lost impact or fabricated downstream outage", p)
		}
	}
}
