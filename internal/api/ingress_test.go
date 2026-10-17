package api

import (
	"testing"
	"time"
)

func TestIngressCountsOnlyHandshakeFailures(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	c := NewIngressCounter(time.Minute)
	c.now = func() time.Time { return now }
	var logged []string
	w := c.Writer(func(line string) { logged = append(logged, line) })
	for _, line := range []string{
		"http: TLS handshake error from 100.74.137.62:44484: EOF",
		"http: TLS handshake error from 100.74.137.62:44485: read tcp 100.111.75.1:7447->100.74.137.62:44485: i/o timeout",
		"http: TLS handshake error from [2605:52c0::1]:443: remote error: tls: bad certificate",
		"http: TLS handshake error from 100.75.127.43:1",
		"http: TLS handshake error from 10.0.0.1:not-a-port: EOF",
		"incident opened: s2-local/path/s2-collector",
		"http: TLS handshake error from bad",
	} {
		if _, err := w.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if len(logged) != 7 || logged[0] != "http: TLS handshake error from 100.74.137.62:44484: EOF" {
		t.Fatalf("writer did not pass every line through: %q", logged)
	}
	got := c.Summary(now)
	if got == nil || got.Failures != 3 || got.WindowSeconds != 60 {
		t.Fatalf("counted %+v", got)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("sources %+v", got.Sources)
	}
	if got.Sources[0].Address != "100.74.137.62" || got.Sources[0].Count != 2 {
		t.Fatalf("busiest source first: %+v", got.Sources)
	}
	if !got.Last.Equal(now) {
		t.Fatalf("last failure %v", got.Last)
	}
}

func TestIngressForgetsOutsideTheWindowAndBoundsSources(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	c := NewIngressCounter(time.Minute)
	c.now = func() time.Time { return now }
	c.observe("http: TLS handshake error from 10.0.0.1:1: EOF")
	if s := c.Summary(now); s == nil || s.Failures != 1 {
		t.Fatal("failure not counted", s)
	}
	now = now.Add(2 * time.Minute)
	if s := c.Summary(now); s != nil {
		t.Fatal("failure outlived its window", s)
	}

	// More addresses than the bound must not grow the map without limit,
	// and the newest failures must survive eviction of the oldest.
	for i := 0; i < maxIngressSources+32; i++ {
		now = now.Add(time.Millisecond)
		c.observe("http: TLS handshake error from 10.1." + itoa(i/256) + "." + itoa(i%256) + ":1: EOF")
	}
	if len(c.sources) > maxIngressSources {
		t.Fatalf("source map grew past its bound: %d", len(c.sources))
	}
	c.observe("http: TLS handshake error from 192.0.2.10:1: EOF")
	if _, ok := c.sources["192.0.2.10"]; !ok {
		t.Fatal("newest source evicted itself")
	}
	if s := c.Summary(now); s == nil || len(s.Sources) > shownIngressSources {
		t.Fatalf("summary not bounded: %+v", s)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
