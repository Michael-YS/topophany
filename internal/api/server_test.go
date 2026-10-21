package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/ledger"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/store"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

const viewerToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const workerToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func fixture(t testing.TB) (*Server, http.Handler, model.Batch) {
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
		{"schema", func(b *model.Batch) { b.Schema = 4 }, 400}, {"contract", func(b *model.Batch) { b.Observations[0].Contract = strings.Repeat("0", 64) }, 409},
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

// A collector clock that steps backward must not make a worker which kept
// its own clock look like it is sampling from the future. A sample still an
// hour ahead of the last collector time is rejected.
func TestCollectorClockStepBackKeepsAdmittingWorkerTime(t *testing.T) {
	srv, h, b := fixture(t)
	start := srv.Now().UTC()
	b.Observations[0].SampledAt = start
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	srv.Now = func() time.Time { return start.Add(-time.Minute) }
	next := testkit.Observation(srv.Config, "home-http", 2, start.Add(2*time.Second), "ok")
	b.Observations = []model.Observation{next}
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal("sample after collector step-back", w.Code, w.Body.String())
	}
	b.Observations = nil
	b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: start.Add(3 * time.Second)}
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal("heartbeat after collector step-back", w.Code, w.Body.String())
	}
	far := testkit.Observation(srv.Config, "home-http", 3, start.Add(time.Hour), "ok")
	b.Observations = []model.Observation{far}
	b.Heartbeat = nil
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 422 {
		t.Fatal("hour-ahead sample", w.Code, w.Body.String())
	}
}

// Keep worker and monotonic time moving while the collector stays a minute behind.
func TestCollectorRollbackContinuousAdmission(t *testing.T) {
	srv, h, b := fixture(t)
	start := srv.Now().UTC()
	elapsed := time.Duration(0)
	srv.admissionClock = func() time.Time { return start.Add(elapsed) }
	srv.Now = func() time.Time { return start }
	b.Observations[0].SampledAt = start
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	srv.Now = func() time.Time { return start.Add(elapsed - time.Minute) }
	for i, delta := range []time.Duration{2 * time.Second, 40 * time.Second, 2 * time.Minute, 10 * time.Minute} {
		elapsed = delta
		at := start.Add(elapsed)
		b.Observations = []model.Observation{testkit.Observation(srv.Config, "home-http", uint64(i+2), at, "ok")}
		b.Heartbeat = &model.Heartbeat{Observer: "home", Boot: "boot", SampledAt: at, Delivery: []model.DeliveryStatus{{Collector: "0123456789abcdef", ReportedAt: at, LastAck: at, LastContact: at, RetryAt: at.Add(30 * time.Second)}}}
		if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
			t.Fatalf("elapsed %s: %d %s", elapsed, w.Code, w.Body)
		}
	}
	b.Heartbeat = nil
	b.Observations = []model.Observation{testkit.Observation(srv.Config, "home-http", 99, start.Add(elapsed+time.Hour), "ok")}
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 422 {
		t.Fatal("future sample accepted", w.Code)
	}
	b.Observations[0].SampledAt = srv.Now().Add(-srv.MaxSampleAge - time.Second)
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 422 {
		t.Fatal("expired sample accepted", w.Code)
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
	srv.Config.Checks[0].Target = "https://example.invalid/health?secret=private#secret-fragment"
	srv.Config.Endpoints[0].Address = "https://user:secret-password@example.invalid/#secret-fragment"
	srv.Config.Paths[0].Proxies = []model.Proxy{{Kind: "ssh", Address: "192.0.2.50:22", SSHUser: "bastion-login-7f3", SSHIdentityFile: "/private/id_ed25519", SSHKnownHosts: "/private/known_hosts"}}
	for _, path := range []string{"/v1/config", "/v1/snapshot"} {
		w := request(h, "GET", path, viewerToken, "", nil)
		for _, secret := range []string{"PRIVATE_API_TOKEN", "secret=private", "secret-fragment", "secret-password", "bastion-login-7f3", "id_ed25519", "known_hosts"} {
			if w.Code != 200 || strings.Contains(w.Body.String(), secret) {
				t.Fatal(path, "configuration credentials leaked", secret, w.Body.String())
			}
		}
	}
	var w *httptest.ResponseRecorder
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
	edited := srv.Config
	edited.Nodes = slices.Clone(edited.Nodes)
	edited.Nodes[0].Notes = "edited"
	srv.Reload(edited, srv.Auth)
	if code, status := srv.ValidateBatch(b, "home"); code != "revision_mismatch" || status != 409 {
		t.Fatal(code, status)
	}
}

// Reload swaps the topology and the tokens at once: a sample of a check
// that now measures something else is refused, a new token is admitted
// and a removed one is not, and no snapshot built before is served again.
func TestReloadSwapsTopologyAndTokens(t *testing.T) {
	srv, h, b := fixture(t)
	if w := request(h, "GET", "/v1/snapshot", viewerToken, "", nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	next := testkit.Config()
	next.Checks[0].HTTP.Status = []int{204}
	next.Nodes[0].Name = "Renamed"
	newViewer := strings.Repeat("e", 64)
	srv.Reload(next, Auth{Schema: 1, Tokens: []Principal{{secure.Hash(newViewer), "viewer", ""}, {secure.Hash(workerToken), "worker", "home"}}})
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 409 || !strings.Contains(w.Body.String(), "contract_mismatch") {
		t.Fatal("a sample of the old contract was accepted", w.Code, w.Body.String())
	}
	b.Observations[0] = testkit.Observation(next, "home-http", 2, srv.Now().Add(-time.Second), "ok")
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal("a sample of the new contract was refused", w.Code, w.Body.String())
	}
	if w := request(h, "GET", "/v1/snapshot", viewerToken, "", nil); w.Code != 401 {
		t.Fatal("a removed token was admitted", w.Code)
	}
	w := request(h, "GET", "/v1/snapshot", newViewer, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Renamed") {
		t.Fatal("the new token or the new topology is not served", w.Code)
	}
}

// A request is served under the tokens that admitted it and the topology
// loaded with them. A worker whose token a reload revokes while it sends
// its batch cannot store a sample of the topology loaded after: the batch
// is refused for the worker to send again, and the revoked token is then
// refused. A viewer admitted before a reload reads the old immutable
// topology, but must retry snapshots that depend on mutable state.
func TestARequestIsServedUnderTheTokensThatAdmittedIt(t *testing.T) {
	srv, h, _ := fixture(t)
	old := srv.Config
	next := testkit.Config()
	next.Checks[0].HTTP.Status = []int{204}
	next.Nodes[0].Name = "Renamed"
	newWorker, newViewer := strings.Repeat("c", 64), strings.Repeat("d", 64)
	auth := Auth{Schema: 1, Tokens: []Principal{{secure.Hash(newViewer), "viewer", ""}, {secure.Hash(newWorker), "worker", "home"}}}
	b := model.Batch{Schema: model.BatchSchema, Project: next.Project, Revision: config.Revision(next), Observations: []model.Observation{testkit.Observation(next, "home-http", 1, srv.Now().Add(-time.Second), "ok")}}
	body := payload(t, b)

	pr, pw := io.Pipe()
	r := httptest.NewRequest("POST", "/v1/observations", pr)
	r.Header.Set("Authorization", "Bearer "+workerToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(w, r)
	}()
	// The first byte is read only once the request is admitted.
	if _, e := pw.Write(body[:1]); e != nil {
		t.Fatal(e)
	}
	srv.Reload(next, auth)
	go func() {
		pw.Write(body[1:])
		pw.Close()
	}()
	<-done
	if w.Code != 503 || !strings.Contains(w.Body.String(), "topology_reloaded") || w.Header().Get("Retry-After") == "" {
		t.Fatal("a batch admitted by a revoked token was not refused to be sent again", w.Code, w.Body.String())
	}
	if got := srv.Store.History("home-http", config.Contracts(next), config.Revision(next), 10); len(got) != 0 {
		t.Fatal("a revoked token stored a sample of the new topology", got)
	}
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", body); w.Code != 401 {
		t.Fatal("a revoked token was admitted again", w.Code, w.Body.String())
	}
	if w := request(h, "POST", "/v1/observations", newWorker, "application/json", body); w.Code != 202 {
		t.Fatal("the batch was refused from the new token", w.Code, w.Body.String())
	}

	// Viewers: one admitted before the reload, one after, and the first
	// again, in either order: immutable topology remains readable under the
	// old authority, while mutable snapshots require a current authority.
	before := httptest.NewRequest("GET", "/v1/config", nil)
	before.Header.Set("Authorization", "Bearer "+viewerToken)
	srv.Reload(old, Auth{Schema: 1, Tokens: []Principal{{secure.Hash(viewerToken), "viewer", ""}, {secure.Hash(workerToken), "worker", "home"}}})
	a, ok := srv.admit(before)
	if !ok {
		t.Fatal("the old viewer was not admitted")
	}
	srv.Reload(next, auth)
	admitted := before.WithContext(context.WithValue(before.Context(), authorityKey{}, a))
	read := func(route func(http.ResponseWriter, *http.Request, authority), r *http.Request) string {
		t.Helper()
		w := httptest.NewRecorder()
		srv.authorize("viewer", route)(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	if strings.Contains(read(srv.topology, admitted), "Renamed") {
		t.Fatal("a viewer admitted before the reload read the topology loaded after it")
	}
	refused := func() {
		t.Helper()
		w := httptest.NewRecorder()
		srv.authorize("viewer", srv.snapshot)(w, admitted)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "topology_reloaded") || w.Header().Get("Retry-After") != "1" {
			t.Fatal("old authority read mutable snapshot state", w.Code, w.Body.String())
		}
	}
	refused()
	if w := request(h, "GET", "/v1/snapshot", newViewer, "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Renamed") {
		t.Fatal("a snapshot built for a viewer admitted before the reload was served after it", w.Code)
	}
	refused()
}

// A collector keeps its ledger from the evidence of all observers, and a
// snapshot carries what it remembers of the incidents in its view.
func TestSnapshotCarriesTheLedger(t *testing.T) {
	srv, h, b := fixture(t)
	srv.Ledger = ledger.New()
	c := srv.Config
	now := srv.Now()
	b.Observations = nil
	for i := uint64(1); i <= 3; i++ {
		b.Observations = append(b.Observations, testkit.Observation(c, "home-http", i, now.Add(-time.Duration(4-i)*time.Second), "fail"))
	}
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	snap, changes, e := srv.Observe(now, true)
	if e != nil || len(changes) != 1 || changes[0].Kind != "opened" || len(snap.Incidents) != 1 {
		t.Fatalf("observed %+v: %v", changes, e)
	}
	read := func(view string) model.Snapshot {
		t.Helper()
		srv.Now = func() time.Time { return now.Add(time.Duration(len(view)+1) * time.Second) }
		w := request(h, "GET", "/v1/snapshot?observer="+view, viewerToken, "", nil)
		var s model.Snapshot
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return s
	}
	if s := read(""); len(s.Ledger) != 1 || s.Ledger[0].ID != snap.Incidents[0].ID || s.Ledger[0].State != "open" {
		t.Fatalf("snapshot ledger %+v", s.Ledger)
	}
	if s := read("home"); len(s.Ledger) != 1 {
		t.Fatalf("home's view lacks its incident: %+v", s.Ledger)
	}
	if s := read("cloud"); len(s.Ledger) != 0 {
		t.Fatalf("cloud's view carries home's incident: %+v", s.Ledger)
	}
}

// The ledger keeps an incident open while its check has not recovered,
// however long ago the samples that confirmed it were taken and however
// few of them the collector still holds.
func TestLedgerKeepsAnIncidentWhoseSamplesAreGone(t *testing.T) {
	c := testkit.Config()
	c.Checks[0].FailureThreshold, c.Checks[0].RecoveryThreshold = 3, 2
	now := time.Now().UTC()
	clock := now
	s, e := store.Open(t.TempDir(), store.Options{Revision: config.Revision(c), Contracts: config.Contracts(c), MaxHistory: 10, Now: func() time.Time { return clock }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	srv := New(c, "test", s, Auth{Schema: 1, Tokens: []Principal{{secure.Hash(workerToken), "worker", "home"}}})
	srv.Now = func() time.Time { return clock }
	srv.Ledger = ledger.New()
	h := srv.Handler()
	start := now.Add(-20 * time.Minute)
	for i := 0; i < 121; i++ {
		outcome := "fail"
		if i >= 3 && i%2 == 0 {
			outcome = "ok"
		}
		clock = start.Add(time.Duration(i) * 10 * time.Second)
		b := model.Batch{Schema: model.BatchSchema, Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{testkit.Observation(c, "home-http", uint64(i+1), clock, outcome)}}
		if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		_, changes, e := srv.Observe(clock, true)
		if e != nil {
			t.Fatal(e)
		}
		for _, ch := range changes {
			if ch.Kind == "closed" {
				t.Fatalf("sample %d (%s) closed the incident: %+v", i+1, outcome, ch)
			}
		}
	}
	snap, _, _ := srv.Observe(clock, true)
	if st := snap.Checks[0]; st.ID != "home-http" || st.Health != "degraded" {
		t.Fatal("a check that never recovered reads", st)
	}
}

// Snapshots say how each check fared over the last day, week and month,
// and /v1/rollups lays its rollups out in slots, both under its public
// contract only.
func TestSnapshotsAndRollupsSayHowChecksFared(t *testing.T) {
	srv, h, b := fixture(t)
	c := srv.Config
	now := srv.Now()
	for start := uint64(1); start <= 120; start += 60 {
		b.Observations = nil
		for i := start; i < start+60; i++ {
			outcome := "ok"
			if i > 100 && i <= 110 {
				outcome = "fail"
			}
			b.Observations = append(b.Observations, testkit.Observation(c, "home-http", i, now.Add(-time.Duration(120-i)*30*time.Second), outcome))
		}
		if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := request(h, "GET", "/v1/snapshot", viewerToken, "", nil)
	var snap model.Snapshot
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &snap) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	var day model.Availability
	for _, cs := range snap.Checks {
		switch {
		case cs.ID == "home-http" && len(cs.Availability) == 3:
			day = cs.Availability[0]
		case cs.ID != "home-http" && cs.Availability != nil:
			t.Fatal("a check never measured has availability", cs.ID)
		}
	}
	// 59.5 minutes measured: 50 of them up, 5 down; the newest stands 0s.
	if day.Window != "24h" || day.Up != 54*60+30 || day.Down != 5*60 || day.Samples != 120 || day.Seconds != 86400 {
		t.Fatalf("a day of a check measured for its last hour: %+v", day)
	}
	w = request(h, "GET", "/v1/rollups?check=home-http&step=5m&span=2h", viewerToken, "", nil)
	var got struct {
		Contract string         `json:"contract"`
		Step     int64          `json:"step_seconds"`
		Total    model.Rollup   `json:"total"`
		Slots    []model.Rollup `json:"slots"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if got.Contract != config.PublicContracts(c)["home-http"] {
		t.Fatal("rollups name another contract", got.Contract)
	}
	up := int64(0)
	for _, slot := range got.Slots {
		up += slot.Up
	}
	// Each slot rounds its time to the second.
	if got.Step != 300 || len(got.Slots) < 24 || got.Total.Up != day.Up || max(up-day.Up, day.Up-up) > int64(len(got.Slots)) || got.Total.Samples != 120 {
		t.Fatalf("rollups: step %d, %d slots, total %+v, slots up %d", got.Step, len(got.Slots), got.Total, up)
	}
	for _, q := range []string{"check=home-http&step=10m", "check=home-http&span=0d", "check=home-http&span=900d", "check=home-http&step=5m&span=7d", "check=home-http&span=7w"} {
		if w := request(h, "GET", "/v1/rollups?"+q, viewerToken, "", nil); w.Code != 400 {
			t.Fatal(q, w.Code)
		}
	}
	if w := request(h, "GET", "/v1/rollups?check=nope", viewerToken, "", nil); w.Code != 404 {
		t.Fatal("unknown check", w.Code)
	}
	if w := request(h, "GET", "/v1/rollups?check=home-http", workerToken, "", nil); w.Code != 403 {
		t.Fatal("a worker read rollups", w.Code)
	}
}

// Viewer reads are served from what a topology gives once, when it is
// loaded: however large the topology, a request, HEAD included, does not
// project it, hash it or derive its contracts again.
func TestViewerReadsDoNotDeriveTheTopologyAgain(t *testing.T) {
	srv, _, _ := fixture(t)
	c := srv.Config
	c.Checks = slices.Clone(c.Checks)
	for i := range 400 {
		x := c.Checks[0]
		x.ID = fmt.Sprintf("extra-%d", i)
		c.Checks = append(c.Checks, x)
	}
	srv.Reload(c, srv.Auth)
	h := srv.Handler()
	now := srv.Now()
	srv.Now = func() time.Time { now = now.Add(time.Second); return now }
	small, _, _ := fixture(t)
	small.Now = srv.Now
	smallHandler := small.Handler()
	for _, path := range []string{"/v1/config", "/v1/history?check=home-http", "/v1/rollups?check=home-http&step=1h"} {
		for _, method := range []string{"GET", "HEAD"} {
			baseline := testing.AllocsPerRun(20, func() {
				if w := request(smallHandler, method, path, viewerToken, "", nil); w.Code != 200 {
					t.Fatal(method, path, w.Code)
				}
			})
			allocs := testing.AllocsPerRun(20, func() {
				if w := request(h, method, path, viewerToken, "", nil); w.Code != 200 {
					t.Fatal(method, path, w.Code)
				}
			})
			if allocs > baseline+32 {
				t.Fatalf("%s %s: allocations grew with topology size: %.0f -> %.0f", method, path, baseline, allocs)
			}
		}
	}
}
