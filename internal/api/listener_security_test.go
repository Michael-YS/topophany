package api

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"testing"
	"time"
)

type sourceAddr string

func (a sourceAddr) Network() string { return "tcp" }
func (a sourceAddr) String() string  { return string(a) }

type sourceConn struct {
	net.Conn
	remote net.Addr
}

func (c sourceConn) RemoteAddr() net.Addr { return c.remote }

type scriptedListener struct{ conns []net.Conn }

func (l *scriptedListener) Accept() (net.Conn, error) {
	if len(l.conns) == 0 {
		return nil, io.EOF
	}
	c := l.conns[0]
	l.conns = l.conns[1:]
	return c, nil
}
func (l *scriptedListener) Close() error   { return nil }
func (l *scriptedListener) Addr() net.Addr { return sourceAddr("127.0.0.1:0") }

func sourcePipe(t *testing.T, source string) (net.Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return sourceConn{Conn: a, remote: sourceAddr(source)}, b
}

func TestConnectionSourceCanonicalization(t *testing.T) {
	for _, tc := range []struct {
		addr net.Addr
		want string
	}{
		{nil, "unknown"},
		{sourceAddr("broken"), "unknown"},
		{sourceAddr("example.invalid:80"), "unknown"},
		{sourceAddr("192.0.2.1:1"), "192.0.2.1"},
		{sourceAddr("192.0.2.1:65535"), "192.0.2.1"},
		{sourceAddr("[::ffff:192.0.2.1]:2"), "192.0.2.1"},
		{sourceAddr("[2001:db8:1:2::1]:3"), "2001:db8:1:2::/64"},
		{sourceAddr("[2001:db8:1:2:ffff:ffff:ffff:ffff]:4"), "2001:db8:1:2::/64"},
		{sourceAddr("[2001:db8:1:3::1]:4"), "2001:db8:1:3::/64"},
		{sourceAddr("[fe80::1%eth0]:5"), "fe80::/64"},
	} {
		if got := connectionSource(tc.addr); got != tc.want {
			t.Errorf("source(%v) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

func TestSourceQuotaRejectsFloodAndAdmitsAnotherSource(t *testing.T) {
	inner := &scriptedListener{}
	var rejected []net.Conn
	for i := 0; i < 128; i++ {
		c, peer := sourcePipe(t, fmt.Sprintf("192.0.2.1:%d", i+1))
		inner.conns = append(inner.conns, c)
		if i >= 16 {
			rejected = append(rejected, peer)
		}
	}
	other, _ := sourcePipe(t, "198.51.100.2:1")
	inner.conns = append(inner.conns, other)
	l := LimitListener(inner, 128).(*limitedListener)
	var admitted []net.Conn
	for i := 0; i < 17; i++ {
		c, err := l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		admitted = append(admitted, c)
		t.Cleanup(func() { _ = c.Close() })
	}
	if got := connectionSource(admitted[16].RemoteAddr()); got != "198.51.100.2" {
		t.Fatalf("flood starved the other source: %s", got)
	}
	for _, c := range rejected {
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		if _, err := c.Read(b[:]); !errors.Is(err, io.EOF) {
			t.Fatalf("excess connection not closed: %v", err)
		}
	}
	if len(l.slots) != 17 || len(l.sources) != 2 {
		t.Fatalf("wrong accounting: slots=%d sources=%v", len(l.slots), l.sources)
	}
	var wg sync.WaitGroup
	for _, c := range admitted {
		for range 4 {
			wg.Add(1)
			go func() { defer wg.Done(); _ = c.Close() }()
		}
	}
	wg.Wait()
	if len(l.slots) != 0 || len(l.sources) != 0 {
		t.Fatal("concurrent close leaked accounting")
	}
}

func TestGlobalAndSourceAdmissionBoundaries(t *testing.T) {
	l := newLimitedListener(&scriptedListener{}, 4, 2, time.Minute)
	if !l.reserve("a") || !l.reserve("a") || l.reserve("a") {
		t.Fatal("source boundary violated")
	}
	if !l.reserve("b") || !l.reserve("c") || l.reserve("d") {
		t.Fatal("global boundary violated")
	}
	if _, ok := l.sources["d"]; ok {
		t.Fatal("rejected source created an unbounded map entry")
	}
	l.release("a")
	if !l.reserve("d") {
		t.Fatal("released global capacity unavailable")
	}
	for _, source := range []string{"a", "b", "c", "d"} {
		l.release(source)
	}
	if len(l.sources) != 0 {
		t.Fatal("inactive sources retained")
	}
	if _, err := l.Accept(); !errors.Is(err, io.EOF) {
		t.Fatalf("listener error not propagated: %v", err)
	}
}

func TestConnectionLifetimeCannotBeExtended(t *testing.T) {
	c, peer := sourcePipe(t, "192.0.2.1:1")
	l := newLimitedListener(&scriptedListener{conns: []net.Conn{c}}, 4, 1, 40*time.Millisecond)
	accepted, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	// Repeated activity and far-future deadlines must not renew the lifetime.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := peer.Write([]byte{1}); err != nil {
				return
			}
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	var b [1]byte
	for {
		if time.Now().After(deadline) {
			t.Fatal("active connection outlived absolute lifetime")
		}
		_ = accepted.SetDeadline(time.Now().Add(time.Hour))
		if _, err := accepted.Read(b[:]); err != nil {
			break
		}
	}
	_ = accepted.Close() // Wait for timer/release synchronization, not a sleep.
	<-done
	if len(l.slots) != 0 || len(l.sources) != 0 {
		t.Fatal("lifetime expiration leaked quota")
	}
}

func TestInvalidConnectionLimits(t *testing.T) {
	for _, maximum := range []int{-1, 0} {
		t.Run(fmt.Sprint(maximum), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid maximum accepted")
				}
			}()
			LimitListener(&scriptedListener{}, maximum)
		})
	}
}

func TestHTTPConnectionReuseRequiresAuthorization(t *testing.T) {
	for _, tls := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", tls), func(t *testing.T) {
			_, h, _ := fixture(t)
			ts := httptest.NewUnstartedServer(h)
			ts.Listener = LimitListener(ts.Listener, 128)
			if tls {
				ts.StartTLS()
			} else {
				ts.Start()
			}
			defer ts.Close()
			client := ts.Client()
			client.Timeout = 3 * time.Second
			for _, tc := range []struct {
				method, path, token string
				status              int
			}{
				{"GET", "/healthz", "", 200},
				{"GET", "/missing", "", 404},
				{"PUT", "/v1/config", viewerToken, 405},
				{"GET", "/v1/config", "", 401},
				{"GET", "/v1/config", "bad", 401},
				{"GET", "/v1/config", workerToken, 403},
			} {
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
				_, readErr := io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if readErr != nil || resp.StatusCode != tc.status || !resp.Close {
					t.Fatalf("%s %s: status=%d close=%v read=%v", tc.method, tc.path, resp.StatusCode, resp.Close, readErr)
				}
			}
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
				_, readErr := io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if readErr != nil || resp.StatusCode != 200 || resp.Close || reused != (i == 1) {
					t.Fatalf("authorized request %d: status=%d close=%v reused=%v read=%v", i, resp.StatusCode, resp.Close, reused, readErr)
				}
			}
		})
	}
}
