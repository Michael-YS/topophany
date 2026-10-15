package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

func TestHTTPConnectionAnonymousAndRejectedRequestsReleaseSlots(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, tc := range []struct {
			name, method, path, token string
			status                    int
			setup                     func(*Server)
		}{
			{name: "health", method: "GET", path: "/healthz", status: 200},
			{name: "missing token", method: "GET", path: "/v1/config", status: 401},
			{name: "invalid token", method: "GET", path: "/v1/config", token: "invalid", status: 401},
			{name: "unknown route", method: "GET", path: "/missing", status: 404},
			{name: "bad method", method: "POST", path: "/healthz", status: 405},
			{name: "wrong role", method: "POST", path: "/v1/observations", token: viewerToken, status: 403},
			{name: "preauth busy", method: "GET", path: "/healthz", status: 429, setup: func(s *Server) {
				for i := 0; i < cap(s.preauthSlots); i++ {
					s.preauthSlots <- struct{}{}
				}
			}},
			{name: "preauth rate", method: "GET", path: "/healthz", status: 429, setup: func(s *Server) {
				s.limits["preauth"] = limit{At: s.Now(), Count: 300}
			}},
			{name: "principal rate", method: "GET", path: "/v1/config", token: viewerToken, status: 429, setup: func(s *Server) {
				s.limits[secure.Hash(viewerToken)] = limit{At: s.Now(), Count: 30}
			}},
		} {
			t.Run(fmt.Sprintf("http2=%v/%s", h2, tc.name), func(t *testing.T) {
				srv, h, _ := fixture(t)
				if tc.setup != nil {
					tc.setup(srv)
				}
				ts := httptest.NewUnstartedServer(h)
				ln := newLimitedListener(ts.Listener, 1, 1, time.Minute)
				ts.Listener = ln
				ts.EnableHTTP2 = h2
				ts.StartTLS()
				defer ts.Close()
				client := ts.Client()
				client.Timeout = 3 * time.Second
				req, err := http.NewRequest(tc.method, ts.URL+tc.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if tc.token != "" {
					req.Header.Set("Authorization", "Bearer "+tc.token)
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_, err = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if err != nil || resp.StatusCode != tc.status || (resp.ProtoMajor == 2) != h2 {
					t.Fatalf("protocol=%s status=%d read=%v", resp.Proto, resp.StatusCode, err)
				}
				if h2 && resp.Header.Get("Connection") != "" {
					t.Fatal("HTTP/1 header sent over HTTP/2")
				}
				// Check the actual admission slot, not just the response header or
				// client's choice to reuse a socket. No client close triggers this.
				deadline := time.Now().Add(3 * time.Second)
				for len(ln.slots) != 0 && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if len(ln.slots) != 0 {
					t.Fatal("rejected/anonymous connection retained the only listener slot")
				}
				// Once released, a viewer must be able to connect and reuse it.
				srv.mu.Lock()
				delete(srv.limits, secure.Hash(viewerToken))
				srv.mu.Unlock()
				for i := 0; i < 2; i++ {
					req, _ := http.NewRequest("GET", ts.URL+"/v1/config", nil)
					req.Header.Set("Authorization", "Bearer "+viewerToken)
					reused := false
					req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
						GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
					}))
					resp, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					_, err = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if err != nil || resp.StatusCode != 200 || reused != (i > 0) {
						t.Fatalf("viewer request %d: status=%d reused=%v read=%v", i, resp.StatusCode, reused, err)
					}
				}
			})
		}
	}
}

func TestHTTPConnectionPreauthRejectionsClose(t *testing.T) {
	srv, h, _ := fixture(t)
	for i := 0; i < cap(srv.preauthSlots); i++ {
		srv.preauthSlots <- struct{}{}
	}
	busy := request(h, "GET", "/healthz", "", "", nil)
	for len(srv.preauthSlots) > 0 {
		<-srv.preauthSlots
	}
	if busy.Code != 429 || busy.Header().Get("Connection") != "close" {
		t.Fatal("preauth concurrency rejection retained an HTTP/1 connection")
	}
	srv.limits["preauth"] = limit{At: srv.Now(), Count: 300}
	rate := request(h, "GET", "/v1/config", "", "", nil)
	if rate.Code != 429 || rate.Header().Get("Connection") != "close" {
		t.Fatal("preauth rate rejection retained an HTTP/1 connection")
	}
	srv.limits[secure.Hash(viewerToken)] = limit{At: srv.Now(), Count: 30}
	rate = request(h, "GET", "/v1/config", viewerToken, "", nil)
	if rate.Code != 429 || rate.Header().Get("Connection") != "close" {
		t.Fatal("a principal's rate rejection retained an HTTP/1 connection")
	}
}

func TestInvalidConnectionParameterCombinations(t *testing.T) {
	for i, tc := range []struct {
		maximum, source int
		lifetime        time.Duration
	}{
		{0, 1, time.Minute},
		{4, 0, time.Minute},
		{4, -1, time.Minute},
		{4, 5, time.Minute},
		{4, 1, 0},
		{4, 1, -time.Second},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid internal connection policy accepted")
				}
			}()
			newLimitedListener(&scriptedListener{}, tc.maximum, tc.source, tc.lifetime)
		})
	}
}
