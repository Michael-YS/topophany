package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/admission"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func externalAPIFixture(t testing.TB) (*Server, http.Handler, model.Batch, model.Config) {
	t.Helper()
	c := testkit.Config()
	c.Sources = []model.Source{{
		ID: "probe-api", Adapter: "synthetic-list", Observer: "home",
		Target: "https://probe.invalid/v1/objects", CredentialEnv: "TOPH_PROBE_TOKEN",
		IntervalSeconds: 30, TimeoutSeconds: 5, StaleSeconds: 90,
		MaxResponseBytes: 1 << 20, MaxPages: 2, MaxObjects: 100, MaxReportBytes: 16 << 10,
	}}
	c.Checks = append(c.Checks,
		model.Check{ID: "vm-resources", Observer: "home", Kind: "external", External: &model.ExternalBinding{Source: "probe-api", Object: "vm/dmit", Report: model.ReportTypeResource, Node: "dmit", MaxAgeSeconds: 120, PrimaryDisk: "root"}},
		model.Check{ID: "web-status", Observer: "home", Kind: "external", Service: "web", External: &model.ExternalBinding{Source: "probe-api", Object: "service/web", Report: model.ReportTypeStatus, Node: "dmit", MaxAgeSeconds: 60}},
	)
	config.Defaults(&c)
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s, err := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c), Contracts: config.Contracts(c), Stale: config.Stale(c), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := Auth{Schema: 1, Tokens: []Principal{{TokenSHA256: secure.Hash(viewerToken), Role: "viewer"}, {TokenSHA256: secure.Hash(workerToken), Role: "worker", Observer: "home"}}}
	srv := New(c, "test", s, a)
	srv.Now = func() time.Time { return now }
	return srv, srv.Handler(), model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{externalObservation(t, c, "vm-resources", 1, now.Add(-time.Minute))}}, c
}

func externalObservation(t testing.TB, c model.Config, id string, seq uint64, at time.Time) model.Observation {
	t.Helper()
	x, ok := config.Check(c, id)
	if !ok {
		t.Fatalf("missing check %s", id)
	}
	report := model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: x.External.Object, SampleID: "sample-1", ObservedAt: at, TimeBasis: model.TimeBasisUpstreamSample, Fields: map[string]string{"memory.total_bytes": model.FieldMissing}}}
	if x.External.Report == model.ReportTypeResource {
		percent := 25.0
		total, used := uint64(8192), uint64(2048)
		report.Resource = &model.ResourceReport{CPU: &model.CPUReport{Percent: &percent}, Disks: []model.DiskReport{{ID: "root", TotalBytes: &total, UsedBytes: &used, Percent: &percent}}}
	} else {
		latency := int64(42)
		report.Status = &model.StatusReport{Status: "up", UpstreamLatencyMS: &latency, ObservationPoint: "probe-1"}
	}
	connected := false
	return model.Observation{
		ID: "home.boot." + externalItoa(seq), Project: c.Project, Contract: config.Contract(c, x), Check: id, Observer: "home", Boot: "boot", Sequence: seq,
		SampledAt: at, Outcome: "ok", Evidence: "reported", Stage: "external_source", Detail: "source read", DurationMS: 5,
		Connectivity: connected, PathEvidence: "external_source",
		External: &model.ExternalObservation{Source: x.External.Source, SourceGeneration: "generation-1", AttemptedAt: at, Contact: true, Report: &report},
	}
}

func externalItoa(n uint64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = digits[n%10]
		n /= 10
	}
	return string(b[i:])
}

func TestExternalObservationAdmissionAndNormalization(t *testing.T) {
	srv, h, b, c := externalAPIFixture(t)
	w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	rows, _ := srv.Store.View()
	if len(rows) != 1 || rows[0].External == nil || rows[0].External.Report == nil {
		t.Fatal("external report not stored")
	}
	if rows[0].External.Report.Resource == nil || rows[0].External.Report.Status != nil {
		t.Fatal("stored report shape changed")
	}
	if rows[0].External.Report.Resource.CPU.Percent == nil || *rows[0].External.Report.Resource.CPU.Percent != 25 {
		t.Fatal("normalized report lost its value")
	}
	state := srv.Store.ExternalView()
	if state == nil || state.Bindings["vm-resources"].ReportSamples != 1 || state.Sources["probe-api"].LastContact.IsZero() {
		t.Fatalf("admitted report did not persist collector state: %+v", state)
	}
	if rows[0].External.AttemptedAt != b.Observations[0].External.AttemptedAt || rows[0].ReceivedAt.IsZero() {
		t.Fatal("attempt time or collector receive time changed")
	}
	// Source failure is an attempt without a report; it is not a target down.
	failure := b
	failure.Observations = []model.Observation{b.Observations[0]}
	failure.Observations[0].ID = "home.boot.2"
	failure.Observations[0].Sequence = 2
	failure.Observations[0].Outcome = "error"
	failure.Observations[0].Code = "source_timeout"
	failure.Observations[0].External = &model.ExternalObservation{Source: "probe-api", SourceGeneration: "generation-1", AttemptedAt: srv.Now().Add(-time.Second), Contact: false}
	w = request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, failure))
	if w.Code != http.StatusAccepted {
		t.Fatal("source attempt was refused", w.Code, w.Body.String())
	}
	// A native schema 3 observation still works.
	native := testkit.Observation(c, "home-http", 3, srv.Now().Add(-time.Second), "ok")
	w = request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{native}}))
	if w.Code != http.StatusAccepted {
		t.Fatal("native schema 3 observation refused", w.Code, w.Body.String())
	}
}

func TestExternalAdmissionRejectsForgedEvidenceAndSchema(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*model.Batch, model.Config)
		status int
		code   string
	}{
		"schema 2": {func(b *model.Batch, c model.Config) { b.Schema = model.BatchSchemaContracts }, 400, "external_requires_schema_3"},
		"schema 4": {func(b *model.Batch, c model.Config) { b.Schema = 4 }, 400, "unsupported_batch_schema"},
		"measured claim": {func(b *model.Batch, c model.Config) {
			b.Observations[0].Evidence = "measured"
		}, 400, "evidence_kind_mismatch"},
		"connectivity claim": {func(b *model.Batch, c model.Config) { b.Observations[0].Connectivity = true }, 400, "external_connectivity_claim"},
		"wrong source":       {func(b *model.Batch, c model.Config) { b.Observations[0].External.Source = "other" }, 400, "external_source_mismatch"},
		"wrong observer":     {func(b *model.Batch, c model.Config) { b.Observations[0].Observer = "cloud" }, 403, "observer_scope_violation"},
		"wrong object":       {func(b *model.Batch, c model.Config) { b.Observations[0].External.Report.Object = "vm/other" }, 400, "external_object_mismatch"},
		"wrong report type": {func(b *model.Batch, c model.Config) {
			latency := int64(1)
			b.Observations[0].External.Report.Resource = nil
			b.Observations[0].External.Report.Fields = nil
			b.Observations[0].External.Report.Status = &model.StatusReport{Status: "up", UpstreamLatencyMS: &latency}
		}, 400, "external_report_type_mismatch"},
		"no attempt": {func(b *model.Batch, c model.Config) { b.Observations[0].External = nil }, 400, "external_attempt_required"},
		"success without report": {func(b *model.Batch, c model.Config) {
			b.Observations[0].External.Report = nil
		}, 400, "external_report_missing"},
		"report on failed contact": {func(b *model.Batch, c model.Config) { b.Observations[0].External.Contact = false }, 400, "external_report_without_contact"},
		"future report": {func(b *model.Batch, c model.Config) {
			b.Observations[0].External.Report.ObservedAt = time.Now().Add(2 * time.Hour)
			b.Observations[0].External.Report.TimeBasis = model.TimeBasisUpstreamSample
		}, 422, "external_report_time_out_of_range"},
		"old attempt": {func(b *model.Batch, c model.Config) {
			b.Observations[0].External.AttemptedAt = time.Now().Add(-48 * time.Hour)
			b.Observations[0].SampledAt = time.Now().Add(-time.Minute)
		}, 422, "external_attempt_time_out_of_range"},
		"report schema": {func(b *model.Batch, c model.Config) { b.Observations[0].External.Report.Schema = 2 }, 400, "unsupported_report_schema"},
		"foreign status field": {func(b *model.Batch, c model.Config) {
			x, _ := config.Check(c, "web-status")
			v := &b.Observations[0]
			v.Check, v.Contract = x.ID, config.Contract(c, x)
			v.External.Report = &model.ExternalReport{ReportMeta: model.ReportMeta{Schema: model.ReportSchema, Object: x.External.Object, ObservedAt: v.SampledAt, TimeBasis: model.TimeBasisUpstreamSample, Fields: map[string]string{"disk.root.percent": model.FieldMissing}}, Status: &model.StatusReport{Status: "up"}}
		}, 400, "unknown_field"},
		"report too large": {func(b *model.Batch, c model.Config) {
			disks := make([]model.DiskReport, 20)
			for i := range disks {
				p := 1.0
				disks[i] = model.DiskReport{ID: "d-" + externalItoa(uint64(i)), Path: strings.Repeat("p", 1000), Percent: &p}
			}
			b.Observations[0].External.Report.Resource.Disks = disks
		}, 400, "report_too_large"},
		"external fields on native": {func(b *model.Batch, c model.Config) {
			b.Observations[0].Check = "home-http"
			b.Observations[0].Contract = config.Contract(c, c.Checks[0])
			b.Observations[0].Evidence = "measured"
		}, 400, "external_binding_mismatch"},
		"unknown submitted verdict": {func(b *model.Batch, c model.Config) {
			b.Observations[0].Outcome = "policy"
		}, 400, "external_attempt_outcome"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, h, b, c := externalAPIFixture(t)
			tc.mutate(&b, c)
			w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
				t.Fatalf("status/code = %d %s, want %d %s", w.Code, w.Body.String(), tc.status, tc.code)
			}
			if rows, _ := srv.Store.View(); len(rows) != 0 {
				t.Fatal("rejected observation was stored")
			}
		})
	}
}

func TestExternalBatchRejectsUnknownCollectorOwnedFields(t *testing.T) {
	_, h, b, _ := externalAPIFixture(t)
	raw := payload(t, b)
	raw = []byte(strings.Replace(string(raw), `"contact":true`, `"contact":true,"data_deadline":"2026-01-01T00:00:00Z"`, 1))
	w := request(h, "POST", "/v1/observations", workerToken, "application/json", raw)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_batch_json") {
		t.Fatal("collector-owned field accepted", w.Code, w.Body.String())
	}
}

func TestExternalReportJSONHasStableNormalizedShape(t *testing.T) {
	_, _, b, _ := externalAPIFixture(t)
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"external"`) || !strings.Contains(string(raw), `"received_at":"0001-01-01T00:00:00Z"`) {
		t.Fatalf("external observation is not versioned as expected: %s", raw)
	}
}

func TestContractSchemasTwoAndThreeRemainReadable(t *testing.T) {
	_, h, native, c := externalAPIFixture(t)
	for _, schema := range []int{model.BatchSchemaContracts, model.BatchSchema} {
		v := testkit.Observation(c, "home-http", uint64(schema), time.Now().Add(-time.Minute), "ok")
		b := model.Batch{Schema: schema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{v}}
		w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
		if w.Code != http.StatusAccepted {
			t.Fatalf("schema %d native batch refused: %d %s", schema, w.Code, w.Body.String())
		}
	}
	_ = native
}

func TestContractSchemaTwoCannotCarryExternalEvidence(t *testing.T) {
	_, h, b, _ := externalAPIFixture(t)
	b.Schema = model.BatchSchemaContracts
	w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "external_requires_schema_3") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestExternalStateAppearsInVersionedSnapshotAndNativeAggregatesIgnoreIt(t *testing.T) {
	srv, h, b, _ := externalAPIFixture(t)
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request(h, "GET", "/v1/snapshot", viewerToken, "", nil)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var snap model.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Schema != model.SnapshotSchema || len(snap.External) != 2 {
		t.Fatalf("snapshot external schema = %d, views = %+v", snap.Schema, snap.External)
	}
	var e model.ExternalBindingView
	for _, view := range snap.External {
		if view.Check == "vm-resources" {
			e = view
		}
	}
	if e.Check != "vm-resources" || e.Health != "up" || e.Report == nil || e.Report.Resource == nil || e.Stats.Samples != 1 || e.DataDeadline.IsZero() || e.ContactDeadline.IsZero() || e.ValidUntil.IsZero() {
		t.Fatalf("external view = %+v", e)
	}
	for _, cs := range snap.Checks {
		if cs.ID == "vm-resources" {
			if cs.Evidence != "reported" || cs.Last != nil || len(cs.Availability) != 0 {
				t.Fatalf("external check state = %+v", cs)
			}
		}
	}
	// The external resource binding is attributed to dmit but not to the
	// measured service web.
	for _, service := range snap.Services {
		for _, id := range service.Checks {
			if id == "vm-resources" {
				t.Fatal("external resource entered measured service aggregation")
			}
		}
	}
	_ = srv
}

func TestAPIAndLocalAdmissionAgreeOnExternalTimeBounds(t *testing.T) {
	cases := map[string]struct {
		mutate func(*model.Batch, model.Config)
		code   string
	}{
		"valid": {},
		"future report": {func(b *model.Batch, _ model.Config) {
			b.Observations[0].External.Report.ObservedAt = time.Now().Add(2 * time.Hour)
		}, "external_report_time_out_of_range"},
		"future envelope": {func(b *model.Batch, _ model.Config) {
			b.Observations[0].SampledAt = time.Now().Add(2 * time.Hour)
		}, "sample_time_out_of_range"},
		"old attempt": {func(b *model.Batch, _ model.Config) {
			b.Observations[0].External.AttemptedAt = time.Now().Add(-48 * time.Hour)
		}, "external_attempt_time_out_of_range"},
		"report after read": {func(b *model.Batch, _ model.Config) {
			b.Observations[0].External.Report.ObservedAt = time.Now().Add(time.Minute)
		}, "external_report_time_out_of_range"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _, b, c := externalAPIFixture(t)
			if tc.mutate != nil {
				tc.mutate(&b, c)
			}
			code, _ := srv.ValidateBatch(b, "home")
			err := srv.Store.Put(b)
			if code == "" {
				if err != nil {
					t.Fatalf("API accepted a report the local store refused: %v", err)
				}
				return
			}
			if tc.code != "" && code != tc.code {
				t.Fatalf("API code = %q, want %q", code, tc.code)
			}
			if err == nil {
				t.Fatalf("local store accepted a report the API refused with %q", code)
			}
			if got := admission.Code(err); got != code {
				t.Fatalf("local code = %q, API code = %q (%v)", got, code, err)
			}
		})
	}
}
