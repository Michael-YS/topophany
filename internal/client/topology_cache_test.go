package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/model"
)

func TestOmittedTopologyRequiresMatchingEndpointRevision(t *testing.T) {
	full := snapshot()
	for _, tc := range []string{"uncached", "wrong-revision", "embedded-topology"} {
		t.Run(tc, func(t *testing.T) {
			slim := full
			slim.TopologyOmitted = true
			slim.Topology = model.Config{}
			if tc == "wrong-revision" {
				slim.Revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}
			if tc == "embedded-topology" {
				slim.Topology = full.Topology
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(slim) }))
			defer srv.Close()
			cl := newClient(t, srv.URL)
			if tc != "uncached" {
				cl.rememberTopology(srv.URL, full)
			} else {
				cl.rememberTopology("another collector", full)
			}
			if _, _, err := cl.Snapshot(context.Background(), ""); err == nil {
				t.Fatal("unbound topology accepted")
			}
		})
	}
}
