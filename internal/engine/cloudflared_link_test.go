package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// A cloudflared connector is one end of the tunnel link: its fresh local
// report decides whether that link holds an edge session, and its
// tunnel_not_ready turns the link down. The public check through the edge
// still proves the edge alone, so the origin behind it stays unproven.
func TestCloudflaredLinkAndPublicEdgeEvidence(t *testing.T) {
	c := testkit.Config()
	c.Nodes = append(c.Nodes, model.Node{ID: "cf", Name: "Cloudflare", Kind: "edge", Provider: "cloudflare"})
	c.Links = append(c.Links, model.Link{ID: "tunnel", From: "home", To: "cf", Transport: "cloudflared"})
	c.Endpoints = append(c.Endpoints, model.Endpoint{ID: "home-public", Node: "home", Address: "https://home.example.com/", Network: "public", Via: []string{"cf"}})
	check := func(id, observer string) model.Check {
		return model.Check{ID: id, Name: id, Observer: observer, IntervalSeconds: 60, TimeoutSeconds: 8, StaleSeconds: 180, FailureThreshold: 3, RecoveryThreshold: 2}
	}
	connector := check("tunnel-connector", "home")
	connector.Link, connector.Kind, connector.Target = "tunnel", "cloudflared", "http://127.0.0.1:20241"
	public := check("home-via-edge", "cloud")
	public.Endpoint, public.Kind = "home-public", "http"
	c.Checks = append(c.Checks, connector, public)
	now := time.Now().UTC()

	up := testkit.Observation(c, connector.ID, 1, now.Add(-time.Second), "ok")
	up.Evidence = "reported"
	up.PathEvidence = model.LocalConnectorPathEvidence
	up.Facts["ready_connections"] = "4"
	up.Facts["ha_connections"] = "4"
	up.Facts["connection_colos"] = "hkg,sin"
	edge := testkit.Observation(c, public.ID, 1, now.Add(-time.Second), "ok")
	edge.Facts["edge_provider"] = "cloudflare"
	edge.Facts["edge_pop"] = "LAX"

	s := Build(c, "collector", "", []model.Observation{up, edge}, nil, now)
	link := findEntity(t, s.Links, "tunnel")
	if link.Health != "up" || !strings.Contains(link.Reason, "service behind it is unproven") {
		t.Fatalf("connector session not shown as reported-up: %+v", link)
	}
	if v := findEntity(t, s.Nodes, "cf"); v.Health != "up" {
		t.Fatalf("edge that answered is not up: %+v", v)
	}
	if v := findEntity(t, s.Nodes, "home"); v.Health == "up" || !strings.Contains(v.Reason, "proves that hop alone") {
		t.Fatalf("a public check through the edge proved the origin: %+v", v)
	}

	rows := []model.Observation{edge}
	for i := 0; i < 3; i++ {
		down := testkit.Observation(c, connector.ID, uint64(2+i), now.Add(time.Duration(i-3)*time.Second), "fail")
		down.Evidence = "reported"
		down.PathEvidence = model.LocalConnectorPathEvidence
		down.Stage, down.Code = "adapter", "tunnel_not_ready"
		down.Facts = map[string]string{"ready_connections": "0"}
		rows = append(rows, down)
	}
	s = Build(c, "collector", "", rows, nil, now)
	link = findEntity(t, s.Links, "tunnel")
	if link.Health != "down" || !strings.Contains(link.Reason, "no established tunnel") {
		t.Fatalf("tunnel_not_ready did not take the link down: %+v", link)
	}
	for _, n := range s.Nodes {
		if n.Health == "down" && n.ID != "home" {
			t.Fatalf("connector failure marked a remote node down: %+v", n)
		}
	}
	if v := findEntity(t, s.Nodes, "cf"); v.Health != "up" {
		t.Fatalf("edge lost its own measured evidence: %+v", v)
	}
	if v := findEntity(t, s.Nodes, "home"); v.Health == "up" {
		t.Fatalf("public edge check promoted the origin after a connector failure: %+v", v)
	}
}

// The connector exemption is narrow: another check's measured accept with no
// protocol response still cannot make a link up.
func TestLinkStillNeedsResponseProof(t *testing.T) {
	c := testkit.Config()
	c.Links = append(c.Links, model.Link{ID: "tcp-link", From: "home", To: "dmit", Transport: "public TCP"})
	c.Checks = append(c.Checks, model.Check{ID: "tcp-link-check", Observer: "home", Link: "tcp-link", Kind: "tcp", Target: "192.0.2.10:22", IntervalSeconds: 60, TimeoutSeconds: 8, StaleSeconds: 180, FailureThreshold: 3, RecoveryThreshold: 2})
	now := time.Now().UTC()
	v := testkit.Observation(c, "tcp-link-check", 1, now.Add(-time.Second), "ok")
	v.Facts["response_proof"] = "none"
	s := Build(c, "collector", "", []model.Observation{v}, nil, now)
	if link := findEntity(t, s.Links, "tcp-link"); link.Health != "unknown" {
		t.Fatalf("a bare TCP accept made a link up: %+v", link)
	}
}

func connectorLinkConfig() model.Config {
	c := testkit.Config()
	c.Links = append(c.Links, model.Link{ID: "tunnel", From: "home", To: "dmit", Transport: "cloudflared"})
	c.Checks = append(c.Checks, model.Check{ID: "tunnel-connector", Name: "cloudflared", Observer: "home", Link: "tunnel", Kind: "cloudflared", Target: "http://127.0.0.1:20241", IntervalSeconds: 60, TimeoutSeconds: 8, StaleSeconds: 180, FailureThreshold: 3, RecoveryThreshold: 2})
	return c
}

func connectorSample(c model.Config, at time.Time, outcome string) model.Observation {
	v := testkit.Observation(c, "tunnel-connector", 1, at, outcome)
	v.Evidence = "reported"
	v.PathEvidence = model.LocalConnectorPathEvidence
	v.Stage = "adapter"
	if outcome == "fail" {
		v.Code = "tunnel_not_ready"
		v.Facts = map[string]string{"ready_connections": "0"}
	} else {
		v.Facts = map[string]string{"ready_connections": "4"}
	}
	return v
}

func TestConnectorLinkExemptionIsNarrow(t *testing.T) {
	now := time.Now().UTC()
	t.Run("other reported telemetry stays unknown", func(t *testing.T) {
		c := connectorLinkConfig()
		c.Checks[len(c.Checks)-1].Kind = "tailscale"
		c.Checks[len(c.Checks)-1].Target = ""
		v := testkit.Observation(c, "tunnel-connector", 1, now.Add(-time.Second), "ok")
		v.Evidence = "reported"
		v.PathEvidence = "peer_local_runtime_reported"
		s := Build(c, "collector", "", []model.Observation{v}, nil, now)
		link := findEntity(t, s.Links, "tunnel")
		if link.Health != "unknown" || !strings.Contains(link.Reason, "runtime reports only") {
			t.Fatalf("non-connector report decided a link: %+v", link)
		}
	})
	t.Run("stale connector is not current", func(t *testing.T) {
		c := connectorLinkConfig()
		v := connectorSample(c, now.Add(-time.Hour), "ok")
		s := Build(c, "collector", "", []model.Observation{v}, nil, now)
		link := findEntity(t, s.Links, "tunnel")
		if link.Health != "unknown" || strings.Contains(link.Reason, "established tunnel") {
			t.Fatalf("stale connector report presented as current: %+v", link)
		}
	})
	t.Run("connector read failure has its own reason", func(t *testing.T) {
		c := connectorLinkConfig()
		v := testkit.Observation(c, "tunnel-connector", 1, now.Add(-time.Second), "error")
		v.Evidence = "reported"
		v.PathEvidence = model.LocalConnectorPathEvidence
		v.Stage, v.Code = "adapter", "metrics_unavailable"
		s := Build(c, "collector", "", []model.Observation{v}, nil, now)
		link := findEntity(t, s.Links, "tunnel")
		if link.Health != "unknown" || !strings.Contains(link.Reason, "local connector read failed") {
			t.Fatalf("monitor error became a report about the link: %+v", link)
		}
	})
	t.Run("mixed measured and connector", func(t *testing.T) {
		c := connectorLinkConfig()
		c.Endpoints[0].Via = []string{}
		c.Checks = append(c.Checks, model.Check{ID: "measured", Observer: "home", Link: "tunnel", Endpoint: "dmit-http", Kind: "http", IntervalSeconds: 60, TimeoutSeconds: 8, StaleSeconds: 180, FailureThreshold: 3, RecoveryThreshold: 2})
		measured := testkit.Observation(c, "measured", 1, now.Add(-time.Second), "ok")
		connector := connectorSample(c, now.Add(-time.Second), "ok")
		s := Build(c, "collector", "", []model.Observation{measured, connector}, nil, now)
		if link := findEntity(t, s.Links, "tunnel"); link.Health != "up" {
			t.Fatalf("mixed evidence: %+v", link)
		}
		connector = connectorSample(c, now.Add(-time.Second), "fail")
		s = Build(c, "collector", "", []model.Observation{measured, connector}, nil, now)
		if link := findEntity(t, s.Links, "tunnel"); link.Health != "degraded" {
			t.Fatalf("one connector failure beside a measured success: %+v", link)
		}
	})
}
