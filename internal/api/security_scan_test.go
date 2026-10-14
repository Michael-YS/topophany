package api

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/engine"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestSlowBodiesAreIsolatedByObserverAndExpire(t *testing.T) {
	srv, h, _ := fixture(t)
	alias := strings.Repeat("c", 64)
	other := strings.Repeat("d", 64)
	srv.Auth.Tokens = append(srv.Auth.Tokens, Principal{secure.Hash(alias), "worker", "home"}, Principal{secure.Hash(other), "worker", "cloud"})
	server := httptest.NewServer(h)
	defer server.Close()
	var conns []net.Conn
	for i := 0; i < 2; i++ {
		conn, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conns = append(conns, conn)
		fmt.Fprintf(conn, "POST /v1/observations HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n{", workerToken)
	}
	deadline := time.Now().Add(time.Second)
	for {
		srv.mu.Lock()
		n := srv.inflight["worker:home"]
		srv.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow bodies did not enter handlers")
		}
		time.Sleep(time.Millisecond)
	}
	if w := request(h, "POST", "/v1/observations", alias, "application/json", []byte(`{}`)); w.Code != 429 {
		t.Fatal("second token bypassed observer concurrency", w.Code)
	}
	for _, path := range []string{"/healthz", "/v1/snapshot"} {
		if w := request(h, "GET", path, viewerToken, "", nil); w.Code != 200 {
			t.Fatal("slow worker blocked read", path, w.Code)
		}
	}
	b := model.Batch{Project: srv.Config.Project, Revision: config.Revision(srv.Config), Heartbeat: &model.Heartbeat{Observer: "cloud", Boot: "boot", SampledAt: srv.Now()}}
	if w := request(h, "POST", "/v1/observations", other, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal("slow worker blocked other observer", w.Code, w.Body.String())
	}
	for _, conn := range conns {
		conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal("body deadline did not produce rejection", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatal(resp.StatusCode)
		}
	}
}

func TestSnapshotCacheDoesNotRepeatStoreWork(t *testing.T) {
	srv, h, b := fixture(t)
	first := request(h, "GET", "/v1/snapshot", viewerToken, "", nil)
	if err := srv.Store.Put(b); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		got := request(h, "GET", "/v1/snapshot", viewerToken, "", nil)
		if got.Code != 200 || !bytes.Equal(first.Body.Bytes(), got.Body.Bytes()) {
			t.Fatal("snapshot rebuilt inside cache window")
		}
	}
	if got := request(h, "GET", "/v1/snapshot?observer=home", viewerToken, "", nil); got.Code != 200 || bytes.Contains(got.Body.Bytes(), []byte(b.Observations[0].ID)) {
		t.Fatal("observer projection did not reuse captured store state", got.Code)
	}
	now := srv.Now().Add(2 * time.Second)
	srv.Now = func() time.Time { return now }
	got := request(h, "GET", "/v1/snapshot", viewerToken, "", nil)
	if got.Code != 200 || bytes.Equal(first.Body.Bytes(), got.Body.Bytes()) {
		t.Fatal("cache did not refresh")
	}
}

func TestLargeValidDiagnosticsFitOfficialSnapshotClient(t *testing.T) {
	c := testkit.Config()
	base := c.Checks[0]
	c.Checks = nil
	// Escaped characters expand sixfold in JSON. One map is shared here only
	// to keep this adversarial contract test's fixture inexpensive.
	facts := map[string]string{}
	for i := 0; i < 32; i++ {
		facts[fmt.Sprintf("fact-%d", i)] = strings.Repeat("&", 2048)
	}
	for i := 0; i < 1200; i++ {
		x := base
		x.ID = fmt.Sprintf("check-%04d", i)
		c.Checks = append(c.Checks, x)
	}
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rows := make([]model.Observation, 0, len(c.Checks))
	for i, x := range c.Checks {
		v := testkit.Observation(c, x.ID, uint64(i+1), now, "policy")
		v.Facts = facts
		v.Detail = strings.Repeat("&", 1024)
		rows = append(rows, v)
	}
	srv := New(c, "test", nil, Auth{})
	srv.Now = func() time.Time { return now }
	for _, v := range rows {
		if code, _ := srv.ValidateBatch(model.Batch{Project: c.Project, Revision: config.Revision(c), Observations: []model.Observation{v}}, v.Observer); code != "" {
			t.Fatal("invalid fixture", code)
		}
		break
	}
	snap := engine.Build(c, "test", "", rows, nil, now)
	raw, err := summaryJSON(snap)
	if err != nil || len(raw) > model.MaxSnapshotBytes {
		t.Fatal("producer violated wire limit", len(raw), err)
	}
	if err := secure.JSONBudget(raw); err != nil {
		t.Fatal("producer violated representation budget", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer server.Close()
	cl, err := client.New(client.Profile{Project: c.Project, Servers: []client.Endpoint{{URL: server.URL, Token: viewerToken}}})
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	// Snapshot freshness must not depend on fixture construction under -race.
	cl.Now = func() time.Time { return now }
	received, _, err := cl.Snapshot(context.Background(), "")
	if err != nil || len(received.Checks) != len(c.Checks) || !received.DetailsOmitted {
		t.Fatal("official client lost valid project summary", err)
	}
}

func TestBidiDiagnosticsAreRejectedAtIngestion(t *testing.T) {
	srv, _, b := fixture(t)
	for _, r := range []rune{0x061c, 0x200e, 0x200f, 0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069} {
		b.Observations[0].Detail = "safe" + string(r) + "label"
		if code, _ := srv.ValidateBatch(b, "home"); code != "unsafe_diagnostic_text" {
			t.Fatalf("U+%04X accepted: %s", r, code)
		}
	}
}

func TestSnapshotViewCacheHasGlobalBudget(t *testing.T) {
	srv, h, _ := fixture(t)
	for i := 0; i < 10; i++ {
		srv.Config.Observers = append(srv.Config.Observers, model.Observer{ID: fmt.Sprintf("view-%d", i), Context: "test", StaleSeconds: 90})
	}
	for i := 0; i < 9; i++ {
		got := request(h, "GET", fmt.Sprintf("/v1/snapshot?observer=view-%d", i), viewerToken, "", nil)
		want := 200
		if i == 8 {
			want = 429
		}
		if got.Code != want {
			t.Fatal(i, got.Code, want)
		}
	}
	if got := request(h, "GET", "/v1/snapshot?observer=view-0", viewerToken, "", nil); got.Code != 200 {
		t.Fatal("cache hit rejected when projection budget full")
	}
}
