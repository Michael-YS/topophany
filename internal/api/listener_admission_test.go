package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSharedSourceCanAuthenticateWhenPendingQuotaIsFull(t *testing.T) {
	for _, hosts := range [][2]string{{"192.0.2.1", "192.0.2.1"}, {"2001:db8:1:2::1", "2001:db8:1:2::2"}} {
		t.Run(hosts[0], func(t *testing.T) {
			inner := &scriptedListener{}
			for i := 0; i < 34; i++ {
				host := hosts[0]
				if i == 16 {
					host = hosts[1]
				}
				c, _ := sourcePipe(t, net.JoinHostPort(host, fmt.Sprint(i+1)))
				inner.conns = append(inner.conns, c)
			}
			l := LimitListener(inner, 128).(*limitedListener)
			for range 16 {
				c, err := l.Accept()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Close() })
			}
			c, err := l.Accept()
			if err != nil {
				t.Fatal("shared source was refused before authentication", err)
			}
			defer c.Close()
			if !authenticateConnection(ConnectionContext(context.Background(), c), "worker:home") {
				t.Fatal("new socket could not authenticate")
			}
			for range 17 {
				pending, err := l.Accept()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { pending.Close() })
			}
			l.mu.Lock()
			defer l.mu.Unlock()
			if c.(*limitedConn).closed || len(l.pending) != 16 || len(l.slots) != 17 || l.principals["worker:home"] != 1 {
				t.Fatal("pending flood evicted authenticated socket or exceeded capacity")
			}
		})
	}
}

func TestSharedSourceFloodAllowsWorkerAndViewerOverHTTP(t *testing.T) {
	for _, protocol := range []string{"http", "tls", "h2"} {
		t.Run(protocol, func(t *testing.T) {
			_, handler, batch := fixture(t)
			ts := httptest.NewUnstartedServer(handler)
			l := LimitListener(ts.Listener, 128).(*limitedListener)
			ts.Listener = l
			ts.Config.ConnContext = ConnectionContext
			ts.Config.ReadHeaderTimeout = time.Second
			ts.EnableHTTP2 = protocol == "h2"
			if protocol == "http" {
				ts.Start()
			} else {
				ts.StartTLS()
			}
			defer ts.Close()
			var sockets []net.Conn
			defer func() {
				for _, c := range sockets {
					c.Close()
				}
			}()
			fill := func() {
				t.Helper()
				for range 16 {
					c, err := net.DialTimeout("tcp", l.Addr().String(), time.Second)
					if err != nil {
						t.Fatal(err)
					}
					sockets = append(sockets, c) // incomplete TLS or HTTP headers
				}
				deadline := time.Now().Add(time.Second)
				for {
					l.mu.Lock()
					n := len(l.pending)
					l.mu.Unlock()
					if n == 16 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("pending source bucket did not fill")
					}
					time.Sleep(time.Millisecond)
				}
			}
			fill()
			client := ts.Client()
			client.Timeout = 3 * time.Second
			request := func(method, path, token string, body []byte, status int) {
				t.Helper()
				req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+token)
				if body != nil {
					req.Header.Set("Content-Type", "application/json")
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal("authenticated request starved by pending connections", err)
				}
				defer resp.Body.Close()
				data, err := io.ReadAll(resp.Body)
				if err != nil || resp.StatusCode != status || (resp.ProtoMajor == 2) != (protocol == "h2") {
					t.Fatalf("%s %s: %s status=%d read=%v body=%s", method, path, resp.Proto, resp.StatusCode, err, data)
				}
			}
			request("POST", "/v1/observations", workerToken, payload(t, batch), 202)
			fill()
			// Use a fresh socket for the viewer, then flood again and verify reuse.
			client.CloseIdleConnections()
			request("GET", "/v1/config", viewerToken, nil, 200)
			fill()
			request("GET", "/v1/config", viewerToken, nil, 200)
		})
	}
}

func TestAuthenticatedConnectionBudgetsPreservePendingCapacity(t *testing.T) {
	inner := &scriptedListener{}
	for i := 0; i < 16; i++ {
		c, _ := sourcePipe(t, fmt.Sprintf("192.0.2.%d:1", i+1))
		inner.conns = append(inner.conns, c)
	}
	l := newLimitedListener(inner, 16, 4, time.Minute)
	for i := 0; i < 16; i++ {
		c, err := l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		if i == 4 && c.(*limitedConn).authenticate("p0") {
			t.Fatal("principal connection quota exceeded")
		}
		principal := fmt.Sprintf("p%d", i/4)
		if got := c.(*limitedConn).authenticate(principal); got != (i < 12) {
			t.Fatal("authenticated global reservation violated", i, got)
		}
	}
	if l.authenticated != 12 || len(l.pending) != 4 || len(l.slots) != 16 {
		t.Fatal("authentication consumed reserved pending capacity")
	}
}

func TestAuthenticationCannotRacePendingEviction(t *testing.T) {
	for range 100 {
		first, _ := sourcePipe(t, "192.0.2.1:1")
		next, _ := sourcePipe(t, "192.0.2.1:2")
		l := newLimitedListener(&scriptedListener{conns: []net.Conn{first, next}}, 4, 1, time.Minute)
		c, err := l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		authenticated := make(chan bool, 1)
		go func() { authenticated <- c.(*limitedConn).authenticate("worker:home") }()
		replacement, err := l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		ok := <-authenticated
		l.mu.Lock()
		closed := c.(*limitedConn).closed
		l.mu.Unlock()
		if ok && closed {
			t.Fatal("eviction closed a socket after successful authentication")
		}
		c.Close()
		replacement.Close()
		if len(l.slots) != 0 || len(l.pending) != 0 || len(l.sources) != 0 || len(l.principals) != 0 || l.authenticated != 0 {
			t.Fatal("promotion/eviction race leaked connection accounting")
		}
	}
}
