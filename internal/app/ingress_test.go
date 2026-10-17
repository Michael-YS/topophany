package app

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/client"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

// A real failed TLS handshake has to travel from net/http's error log
// through the collector's counter into the snapshot viewers read. The
// ingress test in internal/api can only attach the counter by hand.
func TestCollectorCountsTLSHandshakeFailures(t *testing.T) {
	dir, _, _, cr := initialized(t)
	cert, key, ca := testkit.Certificate(t, "localhost")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &lockedWriter{}
	done := make(chan error, 1)
	a := &App{In: strings.NewReader(""), Out: io.Discard, Err: log}
	go func() {
		done <- a.Run(ctx, []string{"serve", "--config", filepath.Join(dir, "topology.json"), "--auth", filepath.Join(dir, "auth.json"), "--data", filepath.Join(dir, "data"), "--listen", "127.0.0.1:0", "--tls-cert", cert, "--tls-key", key})
	}()
	re := regexp.MustCompile(`listening on (127\.0\.0\.1:\d+)`)
	var addr string
	testkit.Wait(t, 3*time.Second, func() bool {
		m := re.FindStringSubmatch(log.String())
		if len(m) > 1 {
			addr = m[1]
			return true
		}
		return false
	})

	// Speak HTTP to a TLS listener: net/http reports a handshake failure.
	conn, e := net.DialTimeout("tcp", addr, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = conn.Write([]byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")); e != nil {
		t.Fatal(e)
	}
	conn.Close()

	p := cr.Profiles["demo"]
	p.Servers[0].URL = "https://" + addr
	p.Servers[0].CAFile = ca
	cl, e := client.New(p)
	if e != nil {
		t.Fatal(e)
	}
	defer cl.Close()
	var got int
	testkit.Wait(t, 5*time.Second, func() bool {
		snap, _, err := cl.Snapshot(context.Background(), "")
		if err == nil && snap.Ingress != nil {
			got = snap.Ingress.Failures
		}
		return got > 0
	})
	if got < 1 {
		t.Fatal("the collector did not report its failed handshake")
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
