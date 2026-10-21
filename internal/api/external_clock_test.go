package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/admission"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/store"
)

func TestExternalAPIAndStoreShareRollbackClock(t *testing.T) {
	for _, initialWrite := range []bool{false, true} {
		t.Run(fmt.Sprint(initialWrite), func(t *testing.T) {
			base, _, _, c := externalAPIFixture(t)
			wall := time.Now().UTC()
			start, tick := wall, wall
			opts := (store.Options{Now: func() time.Time { return wall }, MonotonicNow: func() time.Time { return tick }}).WithTopology(c)
			st, err := store.Open(t.TempDir(), opts)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			srv := New(c, "clock-test", st, base.Auth)
			srv.Now = func() time.Time { return wall }
			srv.admissionClock = func() time.Time { return tick }
			h := srv.Handler()
			makeBatch := func(sequence uint64, at time.Time) model.Batch {
				v := externalObservation(t, c, "vm-resources", sequence, at)
				v.External.Report.SampleID = fmt.Sprint(sequence)
				return model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{v}}
			}
			first := makeBatch(1, start)
			if initialWrite {
				if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, first)); w.Code != http.StatusAccepted {
					t.Fatal(w.Code, w.Body.String())
				}
			} else if code, _ := srv.ValidateBatch(first, "home"); code != "" {
				t.Fatal(code)
			}
			// Validation may be the only use of the clock before rollback;
			// storage must still retain that trusted time on its first write.
			for i, elapsed := range []time.Duration{time.Minute, 10 * time.Minute, 2 * time.Hour} {
				wall, tick = start.Add(elapsed-time.Hour), start.Add(elapsed)
				b := makeBatch(uint64(i+2), tick)
				if code, _ := srv.ValidateBatch(b, "home"); code != "" {
					t.Fatal("API rejected advancing worker time", code)
				}
				if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != http.StatusAccepted {
					t.Fatalf("API and storage disagree after %s: %d %s", elapsed, w.Code, w.Body.String())
				}
			}
			before := st.ExternalView().Bindings["vm-resources"].ReportSamples
			far := makeBatch(99, tick.Add(time.Hour))
			if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, far)); w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("API admitted far-future evidence: %d %s", w.Code, w.Body.String())
			}
			if err := st.Put(far); admission.Code(err) != "sample_time_out_of_range" {
				t.Fatalf("direct store admitted far-future evidence: %v", err)
			}
			if st.ExternalView().Bindings["vm-resources"].ReportSamples != before {
				t.Fatal("refused future evidence changed report state")
			}
		})
	}
}
