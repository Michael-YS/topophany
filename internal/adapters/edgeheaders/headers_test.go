package edgeheaders_test

import (
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/adapters/edgeheaders"
)

func TestReportedLabelBoundsAndCacheVocabulary(t *testing.T) {
	out := map[string]string{}
	edgeheaders.SetFact(out, "edge_pop", "  NRT\x1b[2J"+strings.Repeat("A", 64))
	if out["edge_pop"] != "NRT2J"+strings.Repeat("A", 19) {
		t.Fatal(out)
	}
	edgeheaders.SetFact(out, "edge_cache", "private-token")
	if _, exists := out["edge_cache"]; exists {
		t.Fatal("unrecognized cache vocabulary exported", out)
	}
	edgeheaders.SetFact(out, "edge_cache", "hit\r\n")
	if out["edge_cache"] != "HIT" {
		t.Fatal(out)
	}
	if edgeheaders.LastHop("upstream, client ") != "client" || edgeheaders.CacheWord("MISS, Hit from cloudfront") != "HIT" || edgeheaders.CacheWord("") != "" {
		t.Fatal("last-hop interpretation changed")
	}
}
