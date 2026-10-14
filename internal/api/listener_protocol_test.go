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

func TestHTTPConnectionHTTP2RemainsReusable(t *testing.T) {
	_, h, _ := fixture(t)
	ts := httptest.NewUnstartedServer(h)
	ts.Listener = LimitListener(ts.Listener, 128)
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	client := ts.Client()
	client.Timeout = 3 * time.Second
	for i, tc := range []struct {
		path, token string
		status      int
	}{
		{"/healthz", "", 200},
		{"/v1/config", "", 401},
		{"/v1/config", viewerToken, 200},
		{"/v1/config", viewerToken, 200},
	} {
		req, err := http.NewRequest("GET", ts.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		reused := false
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
		}))
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if readErr != nil || response.ProtoMajor != 2 || response.StatusCode != tc.status || response.Header.Get("Connection") != "" || reused != (i > 0) {
			t.Fatalf("request %d: protocol=%s status=%d reused=%v read=%v", i, response.Proto, response.StatusCode, reused, readErr)
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
