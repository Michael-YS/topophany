package api

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

func TestViewerCannotReserveAllSnapshotBuilds(t *testing.T) {
	srv, h, _ := fixture(t)
	for i := 0; i < 10; i++ {
		srv.Config.Observers = append(srv.Config.Observers, model.Observer{ID: fmt.Sprintf("v%d", i), Context: "test", StaleSeconds: 90})
	}
	token := strings.Repeat("e", 64)
	srv.Auth.Tokens = append(srv.Auth.Tokens, Principal{TokenSHA256: secure.Hash(token), Role: "viewer"})
	for i := 0; i < 10; i++ {
		got := request(h, "GET", fmt.Sprintf("/v1/snapshot?observer=v%d", i), viewerToken, "", nil)
		want := 200
		if i >= 2 {
			want = 429
		}
		if got.Code != want {
			t.Fatal(i, got.Code)
		}
	}
	if got := request(h, "GET", "/v1/snapshot?observer=v9", token, "", nil); got.Code != 200 {
		t.Fatal("second viewer starved", got.Code)
	}
}

func TestConnectionLimitCoversPartialHeadersAndReleases(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the global cap independently; source quotas have separate tests.
	bounded := newLimitedListener(ln, 2, 2, maxConnectionLifetime)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }), ReadHeaderTimeout: 100 * time.Millisecond}
	defer server.Close()
	go server.Serve(bounded)
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fmt.Fprint(c, "GET /")
	}
	deadline := time.Now().Add(time.Second)
	for len(bounded.slots) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("connections not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	extra, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	extra.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := extra.Read(b[:]); err == nil {
		t.Fatal("excess connection stayed open")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("excess connection was not rejected")
	}
	for len(bounded.slots) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("expired slots leaked")
		}
		time.Sleep(time.Millisecond)
	}
	response, err := http.Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
}

type blockedWriter struct {
	header  http.Header
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockedWriter) Header() http.Header         { return w.header }
func (w *blockedWriter) WriteHeader(int)             { w.once.Do(func() { close(w.entered) }); <-w.release }
func (w *blockedWriter) Write(p []byte) (int, error) { return len(p), nil }

// Anonymous traffic shares one budget, which it can use up; a worker and
// a viewer that authenticate are counted under budgets of their own and
// are never refused for it, nor for the slots anonymous requests hold.
func TestAnonymousTrafficCannotRefuseAuthenticatedClients(t *testing.T) {
	srv, h, b := fixture(t)
	for _, path := range []string{"/healthz", "/v1/snapshot", "/missing"} {
		for i := 0; i < 120; i++ {
			request(h, "GET", path, "", "", nil)
		}
	}
	if got := request(h, "GET", "/v1/snapshot", "not-a-token", "", nil); got.Code != 429 {
		t.Fatal("anonymous traffic was not bounded", got.Code)
	}
	for i := 0; i < cap(srv.preauthSlots); i++ {
		srv.preauthSlots <- struct{}{}
	}
	defer func() {
		for len(srv.preauthSlots) > 0 {
			<-srv.preauthSlots
		}
	}()
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal("a worker was refused for anonymous traffic", w.Code, w.Body.String())
	}
	if w := request(h, "GET", "/v1/snapshot", viewerToken, "", nil); w.Code != 200 {
		t.Fatal("a viewer was refused for anonymous traffic", w.Code, w.Body.String())
	}
	// A principal is bounded by its own budget, whatever it asks for.
	var got int
	for i := 0; i < 31; i++ {
		got = request(h, "GET", "/missing", viewerToken, "", nil).Code
	}
	if got != 429 {
		t.Fatal("an authenticated client was not bounded", got)
	}
	if w := request(h, "POST", "/v1/observations", workerToken, "application/json", payload(t, b)); w.Code != 202 {
		t.Fatal("one principal's budget refused another", w.Code)
	}
}
func TestPreauthenticationConcurrencyAndRateBounds(t *testing.T) {
	for _, path := range []string{"/healthz", "/v1/snapshot"} {
		srv, h, _ := fixture(t)
		release := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < cap(srv.preauthSlots); i++ {
			w := &blockedWriter{header: make(http.Header), entered: make(chan struct{}), release: release}
			wg.Add(1)
			go func() { defer wg.Done(); h.ServeHTTP(w, httptest.NewRequest("GET", path, nil)) }()
			<-w.entered
		}
		got := request(h, "GET", path, "", "", nil)
		close(release)
		wg.Wait()
		if got.Code != 429 {
			t.Fatal("preauth concurrency unbounded", path, got.Code)
		}
		if got := request(h, "GET", "/v1/snapshot", viewerToken, "", nil); got.Code != 200 {
			t.Fatal("slots leaked", got.Code)
		}
		for i := 0; i < 301; i++ {
			got = request(h, "GET", path, "", "", nil)
		}
		if got.Code != 429 {
			t.Fatal("preauth rate unbounded")
		}
	}
}
