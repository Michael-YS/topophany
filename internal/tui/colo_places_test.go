package tui

import (
	"testing"
)

// A colo clustered into a site with a declared node adds nothing to the
// site's title: Cloudflare's "Frankfurt-am-Main" is not a second place beside
// the operator's "Frankfurt". A site of only colos keeps the colo's city.
func TestSiteTitleIsNotSpelledTwiceByAColo(t *testing.T) {
	nodes := []mapNode{
		{ID: "dc", Name: "DC", Kind: "host", Health: "up", Located: true, Lat: 50.11, Lon: 8.68, Place: "Frankfurt"},
		{ID: "colo:cf:FRA", Name: "FRA", Kind: "colo", Signal: "carried", Located: true, Lat: 50.03, Lon: 8.57, Place: "Frankfurt-am-Main"},
		{ID: "ny", Name: "NY", Kind: "host", Health: "up", Located: true, Lat: 40.71, Lon: -74.01, Place: "New York"},
		{ID: "colo:cf:EWR", Name: "EWR", Kind: "colo", Signal: "carried", Located: true, Lat: 40.69, Lon: -74.17, Place: "Newark"},
		{ID: "colo:cf:LAX", Name: "LAX", Kind: "colo", Signal: "carried", Located: true, Lat: 33.94, Lon: -118.41, Place: "Los Angeles"},
	}
	points := [][2]float64{}
	for _, n := range nodes {
		points = append(points, [2]float64{n.Lat, n.Lon})
	}
	g := fitGeo(points, 240, 160, 2, 0, nil)
	got := map[string]string{}
	for _, s := range geoSites(nodes, g, mapFocus{}) {
		got[nodes[s.nodes[0]].ID] = siteTitle(s, nodes)
	}
	want := map[string]string{"dc": "Frankfurt", "ny": "New York", "colo:cf:LAX": "Los Angeles"}
	if len(got) != len(want) {
		t.Fatalf("sites %v, want %v", got, want)
	}
	for id, title := range want {
		if got[id] != title {
			t.Errorf("site of %s is titled %q, want %q", id, got[id], title)
		}
	}

	// The same on the virtual map's cards: the declared place names the card
	// and the colo's city only a card of colos.
	titles := map[string]string{}
	for _, c := range virtualCards(nodes) {
		titles[nodes[c.rows[0]].ID] = c.title()
	}
	for id, title := range want {
		if titles[id] != title {
			t.Errorf("card of %s is titled %q, want %q", id, titles[id], title)
		}
	}

	// A card whose declared node names no place falls back on the colo's
	// city, which reads better than coordinates.
	nodes[0].Place = ""
	for _, c := range virtualCards(nodes[:2]) {
		if title := c.title(); title != "Frankfurt-am-Main" {
			t.Errorf("placeless card is titled %q", title)
		}
	}
}
