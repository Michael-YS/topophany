package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

const viewerToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const workerToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func fixture(t *testing.T) (*Server, http.Handler, model.Batch) {
	t.Helper()
	c := testkit.Config()
	now := time.Now().UTC()
	s, e := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c), Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	a := Auth{Schema: 1, Tokens: []Principal{{secure.Hash(viewerToken), "viewer", ""}, {secure.Hash(workerToken), "worker", "home"}}}
	srv := New(c, "test", s, a)
	srv.Now = func() time.Time { return now }
	v := testkit.Observation(c, "home-http", 1, now.Add(-time.Minute), "ok")
	return srv, srv.Handler(), model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{v}}
}
func request(h http.Handler, method, path, token, content string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if content != "" {
		r.Header.Set("Content-Type", content)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func payload(t *testing.T, b any) []byte {
	t.Helper()
	v, e := json.Marshal(b)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestRolesAndNoManagementSurface(t *testing.T) {
	_, h, _ := fixture(t)
	for _, tc := range []struct {
		method, path, token string
		want                int
	}{{"GET", "/healthz", "", 200}, {"GET", "/v1/snapshot", "", 401}, {"GET", "/v1/snapshot", "bad", 401}, {"GET", "/v1/snapshot", workerToken, 403}, {"GET", "/v1/snapshot", viewerToken, 200}, {"POST", "/v1/observations", viewerToken, 403}, {"PUT", "/v1/config", viewerToken, 405}, {"POST", "/v1/actions", viewerToken, 404}} {
		t.Run(tc.method+tc.path+tc.token[:min(2, len(tc.token))], func(t *testing.T) {
			w := request(h, tc.method, tc.path, tc.token, "", nil)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing safe response headers")
			}
		})
	}
}
func TestIngestDurableIdempotentAndHistory(t *testing.T) {
	srv, h, b := fixture(t)
	for i := 0; i < 2; i++ {
		w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	rows, _ := srv.Store.View()
	if len(rows) != 1 || rows[0].SampledAt != b.Observations[0].SampledAt || rows[0].ReceivedAt != srv.Now() {
		t.Fatal("duplicate or sampling time changed", rows)
	}
	w := request(h, "GET", "/v1/history?check=home-http&limit=1", viewerToken, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), rows[0].ID) {
		t.Fatal(w.Code, w.Body.String())
	}
	b.Observations[0].Outcome = "fail"
	w = request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
	if w.Code != 409 {
		t.Fatal("conflicting identity accepted", w.Code)
	}
	if rows, _ = srv.Store.View(); len(rows) != 1 || rows[0].Outcome != "ok" {
		t.Fatal("conflict damaged existing result")
	}
}
func TestBatchValidationContract(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*model.Batch)
		status int
	}{
		{"project", func(b *model.Batch) { b.Project = "other" }, 409}, {"revision_format", func(b *model.Batch) { b.Revision = "old" }, 400},
		{"schema", func(b *model.Batch) { b.Schema = 3 }, 400}, {"contract", func(b *model.Batch) { b.Observations[0].Contract = strings.Repeat("0", 64) }, 409},
		{"contract_with_revision", func(b *model.Batch) { b.Observations[0].Revision = b.Revision }, 409},
		{"legacy_revision", func(b *model.Batch) {
			b.Schema, b.Revision = 0, strings.Repeat("a", 64)
			b.Observations[0].Contract, b.Observations[0].Revision = "", b.Revision
		}, 409},
		{"legacy_with_contract", func(b *model.Batch) { b.Schema = 1 }, 409},
		{"observer", func(b *model.Batch) { b.Observations[0].Observer = "cloud" }, 403}, {"foreign_check", func(b *model.Batch) { b.Observations[0].Check = "cloud-http" }, 403},
		{"unknown_check", func(b *model.Batch) { b.Observations[0].Check = "missing" }, 403}, {"observation_contract", func(b *model.Batch) { b.Observations[0].Project = "wrong" }, 409},
		{"zero_sequence", func(b *model.Batch) { b.Observations[0].Sequence = 0 }, 400}, {"id", func(b *model.Batch) { b.Observations[0].ID = "forged" }, 400}, {"boot", func(b *model.Batch) { b.Observations[0].Boot = "../bad" }, 400},
		{"future", func(b *model.Batch) { b.Observations[0].SampledAt = time.Now().Add(time.Hour) }, 422}, {"old", func(b *model.Batch) { b.Observations[0].SampledAt = time.Now().Add(-48 * time.Hour) }, 422}, {"zero_time", func(b *model.Batch) { b.Observations[0].SampledAt = time.Time{} }, 422},
		{"received_owned", func(b *model.Batch) { b.Observations[0].ReceivedAt = time.Now() }, 400}, {"outcome", func(b *model.Batch) { b.Observations[0].Outcome = "definitely_gfw" }, 400}, {"evidence", func(b *model.Batch) { b.Observations[0].Evidence = "inferred" }, 400},
		{"duration", func(b *model.Batch) { b.Observations[0].DurationMS = -1 }, 400}, {"oversized_detail", func(b *model.Batch) { b.Observations[0].Detail = strings.Repeat("x", 1025) }, 400}, {"ansi", func(b *model.Batch) { b.Observations[0].Detail = "\x1b[2J" }, 400},
		{"fact_budget", func(b *model.Batch) { b.Observations[0].Facts["bad"] = strings.Repeat("x", 2049) }, 400}, {"fact_control", func(b *model.Batch) { b.Observations[0].Facts["bad"] = "\x1b]0;spoof\a" }, 400},
		{"timing", func(b *model.Batch) { b.Observations[0].Timings = map[string]int64{"tcp": -1} }, 400}, {"empty", func(b *model.Batch) { b.Observations = nil }, 400},
		{"too_many", func(b *model.Batch) {
			for len(b.Observations) < 101 {
				b.Observations = append(b.Observations, b.Observations[0])
			}
		}, 400},
		{"heartbeat_scope", func(b *model.Batch) {
			b.Heartbeat = &model.Heartbeat{Observer: "cloud", Boot: "boot", SampledAt: time.Now()}
		}, 403},
		{"heartbeat_clock", func(b *model.Batch) {
			b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: time.Now().Add(time.Hour)}
		}, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, h, b := fixture(t)
			tc.mutate(&b)
			w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if rows, _ := srv.Store.View(); len(rows) != 0 {
				t.Fatal("partially applied invalid batch")
			}
		})
	}
}
func TestInvalidSecondObservationLeavesBatchUncommitted(t *testing.T) {
	srv, h, b := fixture(t)
	bad := b.Observations[0]
	bad.Check = "cloud-http"
	b.Observations = append(b.Observations, bad)
	w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
	rows, _ := srv.Store.View()
	if w.Code != 403 || len(rows) != 0 {
		t.Fatal(w.Code, rows)
	}
}
func TestHeartbeatNotANewMeasurement(t *testing.T) {
	srv, h, b := fixture(t)
	b.Observations = nil
	b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: srv.Now()}
	w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/v1/snapshot?observer=home", viewerToken, "", nil)
	var snap model.Snapshot
	if e := json.Unmarshal(w.Body.Bytes(), &snap); e != nil {
		t.Fatal(e)
	}
	for _, v := range snap.Checks {
		if v.Health != "unknown" {
			t.Fatal("heartbeat promoted unchecked target", v)
		}
	}
}
func TestAPIReadBoundariesAndRedaction(t *testing.T) {
	srv, h, _ := fixture(t)
	srv.Config.Checks[0].HTTP.BearerEnv = "PRIVATE_API_TOKEN"
	srv.Config.Checks[0].Target = "https://example.invalid/health?secret=private"
	w := request(h, "GET", "/v1/config", viewerToken, "", nil)
	if strings.Contains(w.Body.String(), "PRIVATE_API_TOKEN") || strings.Contains(w.Body.String(), "secret=private") {
		t.Fatal("configuration credentials leaked", w.Body.String())
	}
	if srv.Config.Checks[0].HTTP.BearerEnv == "" {
		t.Fatal("redaction mutated runtime")
	}
	for _, p := range []string{"/v1/snapshot?observer=missing", "/v1/history?check=home-http&limit=0", "/v1/history?check=home-http&limit=501", "/v1/history?check=home-http&limit=x"} {
		w = request(h, "GET", p, viewerToken, "", nil)
		if w.Code != 400 {
			t.Fatal(p, w.Code)
		}
	}
	w = request(h, "GET", "/v1/history?check=missing", viewerToken, "", nil)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestInputBudgetsAndRateLimit(t *testing.T) {
	srv, h, b := fixture(t)
	w := request(h, "POST", "/v1/observations", workerToken, "text/plain", payload(t, b))
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
	for _, data := range [][]byte{[]byte(`{"unknown":1}`), append(payload(t, b), []byte(`{}`)...), []byte(strings.Repeat(" ", 1<<20+1))} {
		w = request(h, "POST", "/v1/observations", workerToken, "application/json", data)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	for i := 0; i < 31; i++ {
		w = request(h, "GET", "/v1/snapshot", viewerToken, "", nil)
	}
	if w.Code != 429 {
		t.Fatal("rate limiting absent", w.Code)
	}
	now := srv.Now().Add(2 * time.Second)
	srv.Now = func() time.Time { return now }
	w = request(h, "GET", "/v1/snapshot", viewerToken, "", nil)
	if w.Code != 200 {
		t.Fatal("rate window did not recover")
	}
	for i := 0; i < cap(srv.slots); i++ {
		srv.slots <- struct{}{}
	}
	w = request(h, "GET", "/healthz", "", "", nil)
	if w.Code != 200 {
		t.Fatal("ingestion slots blocked health")
	}
}
func TestStorageFailureIsNotAcknowledged(t *testing.T) {
	srv, h, b := fixture(t)
	srv.Store.Close()
	w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b))
	if w.Code != 503 {
		t.Fatal("acknowledged unstored data", w.Code)
	}
}
func TestAuthFilesAndValidation(t *testing.T) {
	c := testkit.Config()
	good := Auth{1, []Principal{{secure.Hash(viewerToken), "viewer", ""}, {secure.Hash(workerToken), "worker", "home"}}}
	p := filepath.Join(t.TempDir(), "auth.json")
	if e := SaveAuth(p, good); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadAuth(p, c); e != nil {
		t.Fatal(e)
	}
	for _, a := range []Auth{{}, {Schema: 2, Tokens: good.Tokens}, {1, []Principal{{"bad", "viewer", ""}}}, {1, append(good.Tokens, good.Tokens[0])}, {1, []Principal{{secure.Hash(viewerToken), "admin", ""}}}, {1, []Principal{{secure.Hash(viewerToken), "viewer", "home"}}}, {1, []Principal{{secure.Hash(workerToken), "worker", "missing"}}}} {
		if e := ValidateAuth(a, c); e == nil {
			t.Fatal("invalid auth accepted", a)
		}
	}
	os.WriteFile(p, []byte(`{"schema":1,"unexpected":true}`), 0600)
	if _, e := LoadAuth(p, c); e == nil {
		t.Fatal("unknown auth field ignored")
	}
}
func FuzzValidateBatch(f *testing.F) {
	c := testkit.Config()
	s := New(c, "test", nil, Auth{})
	s.Now = func() time.Time { return time.Unix(2000000000, 0) }
	f.Add([]byte(`{}`))
	b := model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "home-http", 1, s.Now(), "ok")}}
	v, _ := json.Marshal(b)
	f.Add(v)
	f.Fuzz(func(t *testing.T, v []byte) {
		if len(v) > 65536 {
			return
		}
		var b model.Batch
		if json.Unmarshal(v, &b) != nil {
			return
		}
		code, status := s.ValidateBatch(b, "home")
		if (code == "") != (status == 0) {
			t.Fatal("invalid status contract")
		}
	})
}

// A worker not yet upgraded sends schema 1 batches, stamped with the
// revision of the whole topology. They count while that revision is the
// collector's own, and read back under the check's public contract.
func TestSchemaOneBatchCountsWhileItsRevisionIsCurrent(t *testing.T) {
	srv, h, b := fixture(t)
	b.Schema = 0
	for i := range b.Observations {
		b.Observations[i].Contract, b.Observations[i].Revision = "", b.Revision
	}
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var snap model.Snapshot
	if w := request(h, "GET", "/v1/snapshot", viewerToken, "", nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &snap) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	public := config.PublicContracts(srv.Config)["home-http"]
	for _, cs := range snap.Checks {
		if cs.ID == "home-http" && (cs.Last == nil || cs.Last.Contract != public || cs.Last.Revision != "") {
			t.Fatal("schema 1 sample not counted under the public contract", cs.Last)
		}
	}
	var history struct {
		Contract string
		Entries  []model.Observation
	}
	w := request(h, "GET", "/v1/history?check=home-http", viewerToken, "", nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &history) != nil || history.Contract != public || len(history.Entries) != 1 || history.Entries[0].Contract != public || history.Entries[0].Revision != "" {
		t.Fatal(w.Code, w.Body.String())
	}
	// Once the topology changes, a schema 1 batch no longer counts at all.
	srv.Config.Nodes[0].Notes = "edited"
	if code, status := srv.ValidateBatch(b, "home"); code != "revision_mismatch" || status != 409 {
		t.Fatal(code, status)
	}
}
