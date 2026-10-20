package view

import (
	"fmt"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

// coloTopology builds a topology whose endpoint reaches an edge and whose
// http checks report the given facts, one check and observer state each. It is
// not validated: the graph must place what the snapshot says, including a code
// no list names.
func coloTopology(facts ...map[string]string) (model.Config, model.Snapshot) {
	c := model.Config{Schema: 1, Project: "colo", Nodes: []model.Node{
		{ID: "win", Name: "Workstation", Kind: "host", Location: &model.Location{Label: "Seattle", Lat: 47.61, Lon: -122.33}},
		{ID: "mac", Name: "Laptop", Kind: "host", Location: &model.Location{Label: "Tokyo", Lat: 35.68, Lon: 139.77}},
		{ID: "cf", Name: "Cloudflare", Kind: "edge", Provider: "cloudflare", Group: "edge"},
		{ID: "app", Name: "App", Kind: "host", Location: &model.Location{Label: "Helsinki", Lat: 60.17, Lon: 24.94}},
	}, Observers: []model.Observer{{ID: "obs-win", Node: "win"}, {ID: "obs-mac", Node: "mac"}},
		Endpoints: []model.Endpoint{{ID: "web", Node: "app", Address: "https://www.example.test/", Network: "public", Via: []string{"cf"}}},
	}
	snap := model.Snapshot{Topology: c}
	for i, f := range facts {
		id := fmt.Sprintf("web-%d", i)
		snap.Topology.Checks = append(snap.Topology.Checks, model.Check{ID: id, Observer: "obs-win", Endpoint: "web", Kind: "http"})
		snap.Checks = append(snap.Checks, model.CheckState{ID: id, Observer: "obs-win", Health: "up", Freshness: "fresh", Last: &model.Observation{Facts: f}})
	}
	return c, snap
}

func mapNodeByID(nodes []MapNode, id string) (MapNode, bool) {
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return MapNode{}, false
}

func mapEdgeByID(edges []MapEdge, id string) (MapEdge, bool) {
	for _, e := range edges {
		if e.ID == id {
			return e, true
		}
	}
	return MapEdge{}, false
}

func TestGraphPlacesTheColoAnEdgeReported(t *testing.T) {
	_, snap := coloTopology(map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT"})
	nodes, edges := (View{Snapshot: snap}).Graph()

	n, ok := mapNodeByID(nodes, "colo:cf:NRT")
	if !ok {
		t.Fatalf("no colo node in %v", nodes)
	}
	if n.Kind != "colo" || n.Name != "NRT" || n.Provider != "cloudflare" || n.Group != "edge" {
		t.Fatalf("colo node is not the reported one: %+v", n)
	}
	if !n.Located || n.Inherited || n.Lat != 35.764702 || n.Lon != 140.386002 || n.Place != "Tokyo" {
		t.Fatalf("colo node is not at its table coordinates: %+v", n)
	}
	if n.Health != "unknown" || n.Signal != "carried" || NodeSignal(n) != "carried" {
		t.Fatalf("a reported colo must claim carried, not measured up: %+v -> %s", n, NodeSignal(n))
	}

	from, ok := mapEdgeByID(edges, "colo:cf:NRT:from:win")
	if !ok || from.Kind != EdgeColo || from.From != "win" || from.To != "colo:cf:NRT" || from.Signal != "carried" || from.Label != "answered from NRT" {
		t.Fatalf("observer hop: %+v, %v", from, ok)
	}
	to, ok := mapEdgeByID(edges, "colo:cf:NRT:to")
	if !ok || to.Kind != EdgeColo || to.From != "colo:cf:NRT" || to.To != "cf" || to.Signal != "carried" || to.Label != "in front of Cloudflare" {
		t.Fatalf("edge hop: %+v, %v", to, ok)
	}
	if EdgeKinds[EdgeColo] != "colo" || len(EdgeKinds) != 5 {
		t.Fatalf("edge kinds do not name the colo hop: %v", EdgeKinds)
	}
	if _, ok := mapNodeByID(nodes, "app"); !ok {
		t.Fatal("placing a colo dropped a declared node")
	}
}

func TestGraphColoFailureIsAffectedNotDown(t *testing.T) {
	_, snap := coloTopology(map[string]string{"edge_provider": "cloudflare", "edge_pop": "HKG"})
	snap.Checks[0].Health = "down"
	nodes, _ := (View{Snapshot: snap}).Graph()
	n, ok := mapNodeByID(nodes, "colo:cf:HKG")
	if !ok {
		t.Fatal("a failing request's colo is not drawn")
	}
	// The edge answered from HKG and something behind it failed: the colo is
	// on a failing path, not proven down itself.
	if NodeSignal(n) != "affected" || n.Affected != true || n.Health != "unknown" {
		t.Fatalf("a failing check marked the colo itself down: %+v -> %s", n, NodeSignal(n))
	}
}

func TestGraphColoForAWorkerOnTheEdge(t *testing.T) {
	_, snap := coloTopology(map[string]string{"edge_provider": "cloudflare", "edge_pop": "FRA"})
	snap.Topology.Endpoints = append(snap.Topology.Endpoints, model.Endpoint{ID: "worker", Node: "cf", Address: "https://worker.example.test/", Network: "public"})
	snap.Topology.Checks = append(snap.Topology.Checks, model.Check{ID: "worker-win", Observer: "obs-win", Endpoint: "worker", Kind: "http"})
	snap.Checks = append(snap.Checks, model.CheckState{ID: "worker-win", Observer: "obs-win", Health: "up", Freshness: "fresh", Last: &model.Observation{Facts: map[string]string{"edge_provider": "cloudflare", "edge_pop": "FRA"}}})
	nodes, edges := (View{Snapshot: snap}).Graph()
	if _, ok := mapNodeByID(nodes, "colo:cf:FRA"); !ok {
		t.Fatalf("a Worker on the edge got no colo: %v", nodes)
	}
	// Both checks are from the same observer node, so the hop is drawn once.
	hops := 0
	for _, e := range edges {
		if e.Kind == EdgeColo && e.To == "colo:cf:FRA" {
			hops++
		}
	}
	if hops != 1 {
		t.Fatalf("the observer hop was drawn %d times, want once", hops)
	}
}

func TestGraphIgnoresColosNotFreshlyReported(t *testing.T) {
	base := map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT"}
	_, fresh := coloTopology(base)
	_, stale := coloTopology(base)
	stale.Checks[0].Freshness = "stale"
	_, paused := coloTopology(base)
	paused.Checks[0].Freshness = "maintenance"
	_, never := coloTopology(base)
	never.Checks[0].Last = nil
	_, malformed := coloTopology(map[string]string{"edge_provider": "cloudflare", "edge_pop": "nrt"})
	_, vercel := coloTopology(map[string]string{"edge_provider": "vercel", "edge_pop": "hnd1"})
	_, withheld := coloTopology(map[string]string{"edge_provider": "cloudflare", "peer_text": "withheld_secret_request"})
	_, leaked := coloTopology(map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT", "peer_text": "withheld_secret_request"})
	for name, tc := range map[string]struct {
		snap   model.Snapshot
		cached bool
	}{
		"stale":     {snap: stale},
		"paused":    {snap: paused},
		"no last":   {snap: never},
		"cached":    {snap: fresh, cached: true},
		"malformed": {snap: malformed},
		"vercel":    {snap: vercel},
		"withheld":  {snap: withheld},
		"leaked":    {snap: leaked},
	} {
		nodes, edges := (View{Snapshot: tc.snap, Cached: tc.cached}).Graph()
		for _, n := range nodes {
			if n.Kind == "colo" {
				t.Fatalf("%s: a colo was drawn from evidence that does not report one: %+v", name, n)
			}
		}
		for _, e := range edges {
			if e.Kind == EdgeColo {
				t.Fatalf("%s: a colo hop was drawn: %+v", name, e)
			}
		}
	}
}

func TestGraphUnknownColoFloatsAndDeduplicates(t *testing.T) {
	facts := map[string]string{"edge_provider": "cloudflare", "edge_pop": "ZZZ"}
	_, snap := coloTopology(facts)
	snap.Topology.Checks = append(snap.Topology.Checks,
		model.Check{ID: "web-mac", Observer: "obs-mac", Endpoint: "web", Kind: "http"},
		model.Check{ID: "web-win-2", Observer: "obs-win", Endpoint: "web", Kind: "http"})
	snap.Checks = append(snap.Checks,
		model.CheckState{ID: "web-mac", Observer: "obs-mac", Health: "up", Freshness: "fresh", Last: &model.Observation{Facts: facts}},
		model.CheckState{ID: "web-win-2", Observer: "obs-win", Health: "down", Freshness: "fresh", Last: &model.Observation{Facts: facts}})
	nodes, edges := (View{Snapshot: snap}).Graph()
	n, ok := mapNodeByID(nodes, "colo:cf:ZZZ")
	if !ok {
		t.Fatal("an unknown code was dropped")
	}
	if n.Located || n.Lat != 0 || n.Lon != 0 || n.Place != "" || n.Name != "ZZZ" {
		t.Fatalf("an unknown code did not float as a pill: %+v", n)
	}
	// One node, two observer hops (one per observer node), and one hop to
	// the edge however many checks saw it.
	from := 0
	for _, e := range edges {
		if e.Kind != EdgeColo {
			continue
		}
		if e.To == "colo:cf:ZZZ" {
			from++
			if e.From != "win" && e.From != "mac" {
				t.Fatalf("observer hop from %s", e.From)
			}
			// The same observer's failing check takes the hop to affected.
			if e.From == "win" && e.Signal != "affected" {
				t.Fatalf("the failing check did not affect the observer hop: %+v", e)
			}
		}
	}
	if from != 2 {
		t.Fatalf("observer hops = %d, want 2: %+v", from, edges)
	}
	// The node keeps what needs attention, and the failing observer does not
	// turn the whole colo into a failure.
	if NodeSignal(n) != "affected" || n.Affected != true {
		t.Fatalf("a mixed report did not keep the worse signal: %+v -> %s", n, NodeSignal(n))
	}
}

func TestGraphCapsColoPseudoNodes(t *testing.T) {
	_, snap := coloTopology(map[string]string{"edge_provider": "cloudflare", "edge_pop": "NRT"})
	// Forty distinct, well-shaped codes from one observer: only 32 colos may
	// be drawn, and every one of them contributes its two hops.
	for i := 0; i < 40; i++ {
		code := fmt.Sprintf("%c%c%c", 'A'+i/26, 'A'+i%26, 'X')
		snap.Topology.Checks = append(snap.Topology.Checks, model.Check{ID: fmt.Sprintf("extra-%d", i), Observer: "obs-win", Endpoint: "web", Kind: "http"})
		snap.Checks = append(snap.Checks, model.CheckState{ID: fmt.Sprintf("extra-%d", i), Health: "up", Freshness: "fresh", Observer: "obs-win", Last: &model.Observation{Facts: map[string]string{"edge_provider": "cloudflare", "edge_pop": code}}})
	}
	nodes, edges := (View{Snapshot: snap}).Graph()
	colos := 0
	for _, n := range nodes {
		if n.Kind == "colo" {
			colos++
		}
	}
	if colos != MaxColoNodes {
		t.Fatalf("drew %d colo nodes, want the cap of %d", colos, MaxColoNodes)
	}
	baseNodes := len(snap.Topology.Nodes)
	if len(nodes) != baseNodes+MaxColoNodes {
		t.Fatalf("the map has %d nodes, want %d declared plus %d colos", len(nodes), baseNodes, MaxColoNodes)
	}
	// One hop from the observer and one to the edge for each drawn colo,
	// plus the endpoint's declared via hop.
	if want := 2*MaxColoNodes + 1; len(edges) != want {
		t.Fatalf("the map has %d edges, want %d", len(edges), want)
	}
}

// Cloudflare's table is not a generic IATA table. A Fastly or CloudFront
// check also reports a three-letter edge_pop, but its code names a different
// network's location and must not be placed with Cloudflare's coordinates.
func TestGraphIgnoresColosFromOtherProviders(t *testing.T) {
	for _, facts := range []map[string]string{
		{"edge_provider": "fastly", "edge_pop": "NRT"},
		{"edge_provider": "cloudfront", "edge_pop": "IAD"},
		{"edge_provider": "vercel", "edge_pop": "hnd1"},
	} {
		_, snap := coloTopology(facts)
		nodes, _ := (View{Snapshot: snap}).Graph()
		for _, n := range nodes {
			if n.Kind == "colo" {
				t.Fatalf("%v produced a Cloudflare colo node %+v", facts, n)
			}
		}
	}
}

// The node cap alone would let many observers multiply the colo hops past the
// map's structural budget, so the observer hops are capped too.
func TestGraphCapsColoHopsWithManyObservers(t *testing.T) {
	c := model.Config{Schema: 1, Project: "colo", Nodes: []model.Node{{ID: "cf", Name: "Cloudflare", Kind: "edge", Provider: "cloudflare"}}, Endpoints: []model.Endpoint{{ID: "web", Node: "cf", Address: "https://www.example.test/", Network: "public", Via: []string{"cf"}}}}
	snap := model.Snapshot{Topology: c}
	for o := 0; o < 16; o++ {
		obs := fmt.Sprintf("obs-%02d", o)
		node := fmt.Sprintf("node-%02d", o)
		c.Nodes = append(c.Nodes, model.Node{ID: node, Name: node, Kind: "host"})
		c.Observers = append(c.Observers, model.Observer{ID: obs, Node: node})
		for k := 0; k < 32; k++ {
			id := fmt.Sprintf("web-%02d-%02d", o, k)
			code := fmt.Sprintf("%c%c%c", 'A'+k/26, 'A'+k%26, 'X')
			c.Checks = append(c.Checks, model.Check{ID: id, Observer: obs, Endpoint: "web", Kind: "http"})
			snap.Checks = append(snap.Checks, model.CheckState{ID: id, Observer: obs, Health: "up", Freshness: "fresh", Last: &model.Observation{Facts: map[string]string{"edge_provider": "cloudflare", "edge_pop": code}}})
		}
	}
	nodes, edges := (View{Snapshot: snap}).Graph()
	coloEdges := 0
	for _, e := range edges {
		if e.Kind == EdgeColo {
			coloEdges++
		}
	}
	if coloEdges > MaxColoHops+MaxColoNodes {
		t.Fatalf("colo hops %d exceed the cap %d", coloEdges, MaxColoHops+MaxColoNodes)
	}
	to := map[string]bool{}
	for _, e := range edges {
		if e.Kind == EdgeColo && e.To == "cf" {
			to[e.From] = true
		}
	}
	for _, n := range nodes {
		if n.Kind == "colo" && !to[n.ID] {
			t.Fatalf("colo node %s has no hop to the edge", n.ID)
		}
	}
}
