package probe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// The real probe and engine agree on a loopback challenge page: the edge
// answered with HTTP 403, and the incident says the origin was not asked.
func TestChallengePageBecomesTheIncident(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Ray", "8f2c1b2a3d4e5f60-NRT")
		w.Header().Set("Cf-Mitigated", "challenge")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "challenge")
	}))
	defer s.Close()
	c := testkit.Config()
	c.Nodes = append(c.Nodes, model.Node{ID: "edge", Name: "Edge", Kind: "edge", Provider: "cloudflare"})
	for i := range c.Endpoints {
		if c.Endpoints[i].ID == "dmit-http" {
			c.Endpoints[i].Via = []string{"edge"}
		}
	}
	for i := range c.Checks {
		if c.Checks[i].ID == "home-http" {
			c.Checks[i].Target = s.URL
			c.Checks[i].FailureThreshold = 1
			c.Checks[i].RecoveryThreshold = 1
		}
	}
	x, ok := config.Check(c, "home-http")
	if !ok {
		t.Fatal("no home-http check")
	}
	o := (Runner{}).Run(context.Background(), c, x)
	if o.Outcome != "fail" || o.Code != "unexpected_status" || o.Facts["edge_mitigation"] != "challenge" {
		t.Fatalf("probe did not record the challenge: %+v", o)
	}
	st := engine.Build(c, "a", "home", []model.Observation{o}, nil, time.Now())
	if len(st.Incidents) != 1 {
		t.Fatalf("challenge produced no incident: %+v", st.Incidents)
	}
	got := st.Incidents[0].Hypothesis
	if !strings.Contains(got, "with HTTP 403") || !strings.Contains(got, "the edge served a challenge page") || !strings.Contains(got, "origin was not asked") {
		t.Fatalf("incident does not name the challenge: %q", got)
	}
}
