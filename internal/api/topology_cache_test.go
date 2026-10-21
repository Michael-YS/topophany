package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestTopologyCacheNegotiationReloadAndClientOwnership(t *testing.T) {
	backend, handler, _ := fixture(t)
	omitted := make(chan bool, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("Accept-Encoding")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		var snap model.Snapshot
		if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
			t.Error(err)
		}
		omitted <- snap.TopologyOmitted
		for name, values := range rec.Header() {
			w.Header()[name] = values
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
	}))
	defer srv.Close()
	cl, err := client.New(client.Profile{Project: "lab", Servers: []client.Endpoint{{URL: srv.URL, Token: viewerToken}}})
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	// Keep this fixture at the JSON layer; gzip is independently exercised.
	read := func() model.Snapshot {
		t.Helper()
		snap, _, err := cl.Snapshot(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	// The custom fixture transport removes negotiation before forwarding.
	first := read()
	first.Topology.Nodes[0].Name = "caller mutation"
	second := read()
	if second.Topology.Nodes[0].Name == "caller mutation" {
		t.Fatal("client cache aliases returned topology")
	}
	next := config.Clone(backend.Config)
	next.Nodes[0].Name = "new topology"
	backend.Reload(next, backend.Auth)
	third := read()
	fourth := read()
	if third.Topology.Nodes[0].Name != "new topology" || third.Revision == second.Revision || fourth.Revision != third.Revision {
		t.Fatal("reload retained old topology")
	}
	for i, want := range []bool{false, true, false, true} {
		if got := <-omitted; got != want {
			t.Fatalf("read %d omitted=%v want %v", i, got, want)
		}
	}
	legacy := request(handler, "GET", "/v1/snapshot", viewerToken, "", nil)
	var snap model.Snapshot
	if err := json.Unmarshal(legacy.Body.Bytes(), &snap); err != nil || snap.TopologyOmitted || snap.Topology.Project != "lab" {
		t.Fatal("legacy compatibility", err)
	}
}
