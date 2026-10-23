package direct

import (
	"github.com/WhitenWhiten/topophany/internal/source"
	"testing"
)

func TestSourceUpdateSharesOnlyUnchangedEvidence(t *testing.T) {
	s, c, now := fleetSession(t, 21, 2)
	old := s.external
	other := c.Checks[20].ID
	reading := resourceReading(now, "new-sample")
	reading.Object = c.Checks[0].External.Object
	reading.Report.Object = reading.Object
	if e := s.Record("audit", c.Sources[0].ID, now, now, []source.Reading{reading}, nil); e != nil {
		t.Fatal(e)
	}
	if old.Bindings[other].Last != s.external.Bindings[other].Last {
		t.Fatal("unrelated source report copied")
	}
	id := c.Checks[0].ID
	if old.Bindings[id].Last.SampleID == "new-sample" || s.external.Bindings[id].Last.SampleID != "new-sample" {
		t.Fatal("update mutated old state or failed to commit")
	}
	fresh, e := s.Snapshot("", now)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range fresh.External {
		if v.Check == id && v.Report != nil {
			*v.Report.Resource.CPU.Percent = 99
		}
	}
	if *s.external.Bindings[id].Last.Resource.CPU.Percent == 99 {
		t.Fatal("snapshot changed committed evidence")
	}
}
