package probe

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func dnsTestCheck(target, transport string) model.Check {
	x := httpCheck("")
	x.Kind = "dns"
	x.Target = target
	x.DNS = model.DNSOptions{Name: "test.example", Type: "A", Transport: transport}
	return x
}

// dotFixture serves DNS-over-TLS with a self-signed certificate for
// dot.test; caFile is that certificate, which the check passes as ca_file.
func dotFixture(t *testing.T, answer func([]byte) []byte) (addr, caFile, name string) {
	t.Helper()
	cert, key, ca := testkit.Certificate(t, "dot.test")
	pair, e := tls.LoadX509KeyPair(cert, key)
	if e != nil {
		t.Fatal(e)
	}
	ln, e := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				var lb [2]byte
				if _, e := io.ReadFull(conn, lb[:]); e != nil {
					return
				}
				q := make([]byte, int(binary.BigEndian.Uint16(lb[:])))
				if _, e := io.ReadFull(conn, q); e != nil {
					return
				}
				out := answer(q)
				frame := binary.BigEndian.AppendUint16(nil, uint16(len(out)))
				_, _ = conn.Write(append(frame, out...))
			}()
		}
	}()
	return ln.Addr().String(), ca, "dot.test"
}

// dohFixture serves DNS-over-HTTPS with the same self-signed CA scheme. The
// returned URL keeps a path, which a DoH target may have.
func dohFixture(t *testing.T, handler http.HandlerFunc) (target, caFile, name string) {
	t.Helper()
	cert, key, ca := testkit.Certificate(t, "doh.test")
	pair, e := tls.LoadX509KeyPair(cert, key)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewUnstartedServer(handler)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s.URL + "/dns-query", ca, "doh.test"
}

func requireTiming(t *testing.T, o model.Observation, key string) {
	t.Helper()
	if _, ok := o.Timings[key]; !ok {
		t.Fatalf("missing %s timing: %+v", key, o.Timings)
	}
}

func TestDNSDoT(t *testing.T) {
	addr, ca, name := dotFixture(t, func(q []byte) []byte { return dnsAnswer(q, "203.0.113.8") })
	x := dnsTestCheck(addr, "dot")
	x.ServerName, x.CAFile = name, ca
	x.DNS.ExpectedIPs = []string{"203.0.113.8"}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Facts["dns_transport"] != "dot" || o.Facts["addresses"] != "203.0.113.8" {
		t.Fatal(o)
	}
	requireTiming(t, o, "tls_ms")
	requireTiming(t, o, "first_byte_ms")

	// The certificate is verified against ca_file and named by server_name;
	// neither may be dropped.
	x = dnsTestCheck(addr, "dot")
	x.ServerName, x.CAFile = "wrong.test", ca
	if o = (Runner{}).Run(context.Background(), testkit.Config(), x); o.Outcome != "fail" || o.Stage != "dns_transport" {
		t.Fatalf("wrong server_name was accepted: %+v", o)
	}
	x = dnsTestCheck(addr, "dot")
	x.ServerName = name
	if o = (Runner{}).Run(context.Background(), testkit.Config(), x); o.Outcome != "fail" || o.Stage != "dns_transport" {
		t.Fatalf("untrusted DoT certificate was accepted: %+v", o)
	}
}

func TestDNSDoH(t *testing.T) {
	type seenRequest struct {
		method, path, query, contentType, accept string
		body                                     []byte
	}
	seen := make(chan seenRequest, 1)
	target, ca, name := dohFixture(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		seen <- seenRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), r.Header.Get("Accept"), body}
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(dnsAnswer(body, "203.0.113.8"))
	})
	x := dnsTestCheck(target, "doh")
	x.ServerName, x.CAFile = name, ca
	x.DNS.ExpectedIPs = []string{"203.0.113.8"}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Facts["dns_transport"] != "doh" || o.Facts["addresses"] != "203.0.113.8" {
		t.Fatal(o)
	}
	requireTiming(t, o, "tls_ms")
	requireTiming(t, o, "first_byte_ms")
	req := <-seen
	if req.method != http.MethodPost || req.path != "/dns-query" || req.query != "" {
		t.Fatalf("DoH request was %s %s?%s", req.method, req.path, req.query)
	}
	if req.contentType != "application/dns-message" || req.accept != "application/dns-message" {
		t.Fatalf("DoH headers were content-type %q, accept %q", req.contentType, req.accept)
	}
	q, e := dnsQuery(binary.BigEndian.Uint16(req.body), "test.example", 1)
	if e != nil || string(q) != string(req.body) {
		t.Fatalf("DoH body was not the wire query: %x", req.body)
	}
}

// dnsRefusal is an answer a resolver can send that no encrypted transport may
// turn into a pass: the same wire message is judged the same way whether it
// arrives in an HTTPS body or a TLS frame.
type dnsRefusal struct {
	name        string
	answer      func(q []byte) []byte
	outcome     string
	stage, code string
}

func sharedDNSRefusals() []dnsRefusal {
	return []dnsRefusal{
		{"wrong transaction ID", func(q []byte) []byte {
			b := dnsAnswer(q, "203.0.113.8")
			b[0] ^= 0xff
			return b
		}, "error", "dns_answer", "unexpected_transaction_id"},
		{"truncated answer", func(q []byte) []byte {
			b := dnsAnswer(q, "203.0.113.8")
			binary.BigEndian.PutUint16(b[2:], 0x8380)
			return b
		}, "fail", "dns_answer", "invalid_or_negative_answer"},
		{"negative answer", func(q []byte) []byte {
			b := dnsAnswer(q)
			b[3] = 0x83
			return b
		}, "fail", "dns_answer", "invalid_or_negative_answer"},
		{"no address answer", func(q []byte) []byte { return dnsAnswer(q) }, "fail", "dns_answer", "no_address_answer"},
	}
}

func TestDNSDoHRefusals(t *testing.T) {
	mutate := func(f func([]byte) []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
			w.Header().Set("Content-Type", "application/dns-message")
			_, _ = w.Write(f(body))
		}
	}
	type refusal struct {
		name        string
		handler     http.HandlerFunc
		outcome     string
		stage, code string
	}
	cases := []refusal{
		{"wrong content type", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>not a DNS answer</html>"))
		}, "fail", "dns_transport", "content_type"},
		{"non-200 status", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}, "fail", "dns_transport", "http_503"},
		{"oversized body", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/dns-message")
			_, _ = w.Write(make([]byte, maxDoHBody+1))
		}, "error", "dns_transport", "response_too_large"},
	}
	for _, r := range sharedDNSRefusals() {
		cases = append(cases, refusal{r.name, mutate(r.answer), r.outcome, r.stage, r.code})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, ca, name := dohFixture(t, tc.handler)
			x := dnsTestCheck(target, "doh")
			x.ServerName, x.CAFile = name, ca
			o := (Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tc.outcome || o.Stage != tc.stage || o.Code != tc.code {
				t.Fatalf("want %s/%s/%s; got %+v", tc.outcome, tc.stage, tc.code, o)
			}
		})
	}
}

// The same answers are refused over DNS over TLS, and its framing adds two of
// its own: the largest frame a two-byte length can announce, and a frame too
// short to hold a DNS header. Both keep the question's transaction ID so that
// it is the message, not the ID check, that is refused.
func TestDNSDoTRefusals(t *testing.T) {
	cases := append(sharedDNSRefusals(),
		dnsRefusal{"largest frame", func(q []byte) []byte {
			b := make([]byte, 65535)
			copy(b, q[:2])
			return b
		}, "fail", "dns_answer", "invalid_or_negative_answer"},
		dnsRefusal{"frame shorter than a header", func(q []byte) []byte {
			return append([]byte(nil), q[:5]...)
		}, "fail", "dns_answer", "invalid_or_negative_answer"},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr, ca, name := dotFixture(t, tc.answer)
			x := dnsTestCheck(addr, "dot")
			x.ServerName, x.CAFile = name, ca
			o := (Runner{}).Run(context.Background(), testkit.Config(), x)
			if o.Outcome != tc.outcome || o.Stage != tc.stage || o.Code != tc.code {
				t.Fatalf("want %s/%s/%s; got %+v", tc.outcome, tc.stage, tc.code, o)
			}
		})
	}
	// The largest frame is also a legal one: a valid answer padded to it is
	// read whole and passes, so the refusal above is the message, not the size.
	addr, ca, name := dotFixture(t, func(q []byte) []byte {
		b := make([]byte, 65535)
		copy(b, dnsAnswer(q, "203.0.113.8"))
		return b
	})
	x := dnsTestCheck(addr, "dot")
	x.ServerName, x.CAFile = name, ca
	if o := (Runner{}).Run(context.Background(), testkit.Config(), x); o.Outcome != "ok" || o.Facts["addresses"] != "203.0.113.8" {
		t.Fatalf("a valid answer in the largest frame was not read: %+v", o)
	}
}

func TestDNSExplicitTCP(t *testing.T) {
	// Only TCP listens on this port, so a passing sample cannot have used
	// UDP or a UDP fallback.
	addr := testkit.Stream(t, func(c net.Conn) {
		var lb [2]byte
		if _, e := io.ReadFull(c, lb[:]); e != nil {
			return
		}
		q := make([]byte, int(binary.BigEndian.Uint16(lb[:])))
		if _, e := io.ReadFull(c, q); e != nil {
			return
		}
		out := dnsAnswer(q, "203.0.113.8")
		frame := binary.BigEndian.AppendUint16(nil, uint16(len(out)))
		_, _ = c.Write(append(frame, out...))
	})
	x := dnsTestCheck(addr, "tcp")
	x.DNS.ExpectedIPs = []string{"203.0.113.8"}
	o := (Runner{}).Run(context.Background(), testkit.Config(), x)
	if o.Outcome != "ok" || o.Facts["dns_transport"] != "tcp" || o.Facts["addresses"] != "203.0.113.8" {
		t.Fatal(o)
	}
}
