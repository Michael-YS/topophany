// Package probe executes bounded checks without changing routing, policy groups,
// TUN settings or services. All diagnostic output is structured and secret-free.
package probe

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

type Command func(context.Context, string, ...string) ([]byte, error)
type Runner struct {
	Command      Command
	ListenPacket func(string, string) (net.PacketConn, error)
}

func (r Runner) Run(ctx context.Context, c model.Config, x model.Check) model.Observation {
	start := time.Now().UTC()
	o := model.Observation{Project: c.Project, Revision: config.Revision(c), Check: x.ID, Observer: x.Observer, SampledAt: start, Outcome: "unknown", Evidence: "measured", Stage: "setup", Facts: map[string]string{}, Timings: map[string]int64{}, PathEvidence: "requested_not_route_verified"}
	if x.Disabled || x.Maintenance {
		o.Detail = "check not executed"
		return o
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(x.TimeoutSeconds)*time.Second)
	defer cancel()
	d := Dialer{Proxies: config.Path(c, x.Path).Proxies, BindIP: x.BindIP}
	target := config.Target(c, x)
	if len(d.Proxies) > 0 {
		o.Facts["transport_hops"] = strconv.Itoa(len(d.Proxies))
		o.PathEvidence = "explicit_proxy_transport"
	}
	o.Facts["connection_semantics"] = "new_client_stream; upstream_core_multiplexing_unknown"
	if x.BindIP != "" {
		o.Facts["bind_ip"] = x.BindIP
	}
	var e error
	switch x.Kind {
	case "http":
		e = r.http(ctx, d, x, target, &o)
	case "tcp", "tls", "ssh-banner":
		e = stream(ctx, d, x, target, &o)
	case "dns":
		e = dns(ctx, x, target, &o)
	case "udp-echo":
		e = udpEcho(ctx, x, target, &o)
	case "icmp":
		e = icmp(ctx, x, target, &o, r.ListenPacket)
	case "mihomo":
		o.Evidence = "reported"
		e = r.mihomo(ctx, d, x, target, &o)
	case "tailscale":
		o.Evidence = "reported"
		e = r.tailscale(ctx, x, &o)
	case "wireguard":
		o.Evidence = "reported"
		e = r.wireguard(ctx, x, &o)
	default:
		e = local("setup", "unsupported_check")
	}
	if e != nil {
		o.Outcome, o.Stage, o.Code = classify(e)
		o.Detail = o.Stage + ": " + o.Code
	}
	o.DurationMS = time.Since(start).Milliseconds()
	return o
}
func pinned(address, ip string) string {
	if ip == "" {
		return address
	}
	_, p, e := net.SplitHostPort(address)
	if e != nil {
		return address
	}
	return net.JoinHostPort(ip, p)
}
func stream(ctx context.Context, d Dialer, x model.Check, target string, o *model.Observation) error {
	began := time.Now()
	conn, e := d.DialContext(ctx, "tcp", pinned(target, x.ResolveIP))
	if e != nil {
		return e
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	o.Timings["connect_ms"] = time.Since(began).Milliseconds()
	switch x.Kind {
	case "tcp":
		if len(d.Proxies) == 1 && d.Proxies[0].Kind == "ssh" {
			// Starting ssh and opening its stdio pipes does not acknowledge
			// authentication or the remote -W connection. Use a response-bearing
			// probe to obtain evidence over this transport.
			o.Outcome = "unknown"
			o.Stage = "connect"
			o.Code = "ssh_forward_unverified"
			o.Detail = "SSH forwarding requires a TLS, HTTP or SSH-banner response to verify connection establishment"
			return nil
		}
		o.Detail = "client-visible TCP connect succeeded; remote application not verified"
		o.Facts["response_proof"] = "none"
	case "tls":
		h, _, _ := net.SplitHostPort(target)
		if x.ServerName != "" {
			h = x.ServerName
		}
		tc, e := TLSConfig(h, x.CAFile)
		if e != nil {
			return e
		}
		t := tls.Client(conn, tc)
		b := time.Now()
		if e = t.HandshakeContext(ctx); e != nil {
			return fail("tls", e)
		}
		o.Timings["tls_ms"] = time.Since(b).Milliseconds()
		o.Facts["tls_version"] = fmt.Sprintf("0x%x", t.ConnectionState().Version)
		o.Detail = "verified TLS handshake; application and proxy authentication not verified"
		o.Facts["response_proof"] = "verified_tls"
	case "ssh-banner":
		br := bufio.NewReader(io.LimitReader(conn, 8193))
		found := false
		for i := 0; i < 20; i++ {
			line, e := br.ReadString('\n')
			if e != nil {
				return fail("ssh_banner", e)
			}
			if len(line) > 255 {
				return &failure{stage: "ssh_banner", code: "line_too_long"}
			}
			if strings.HasPrefix(line, "SSH-2.0-") || strings.HasPrefix(line, "SSH-1.99-") {
				found = true
				break
			}
		}
		if !found {
			return &failure{stage: "ssh_banner", code: "invalid_banner"}
		}
		o.Detail = "SSH protocol banner received; host key and authentication not verified"
		o.Facts["response_proof"] = "ssh_banner"
	}
	o.Outcome = "ok"
	o.Connectivity = true
	o.Stage = x.Kind
	return nil
}
func (r Runner) http(ctx context.Context, d Dialer, x model.Check, target string, o *model.Observation) error {
	u, e := url.Parse(target)
	if e != nil {
		return local("setup", "invalid_url")
	}
	name := u.Hostname()
	if x.ServerName != "" {
		name = x.ServerName
	}
	tc, e := TLSConfig(name, x.CAFile)
	if e != nil {
		return e
	}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: tc, ForceAttemptHTTP2: false, MaxResponseHeaderBytes: 32768, ResponseHeaderTimeout: time.Duration(x.TimeoutSeconds) * time.Second, DialContext: func(ctx context.Context, n, a string) (net.Conn, error) {
		return d.DialContext(ctx, n, pinned(a, x.ResolveIP))
	}}
	defer tr.CloseIdleConnections()
	client := http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var mu sync.Mutex
	traceTimings := map[string]int64{}
	traceFacts := map[string]string{}
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for k, v := range traceTimings {
			o.Timings[k] = v
		}
		for k, v := range traceFacts {
			o.Facts[k] = v
		}
	}()
	phase := "http"
	var dnsStart, tlsStart time.Time
	requestStart := time.Now()
	trace := &httptrace.ClientTrace{DNSStart: func(httptrace.DNSStartInfo) { mu.Lock(); defer mu.Unlock(); dnsStart = time.Now(); phase = "dns" }, DNSDone: func(info httptrace.DNSDoneInfo) {
		mu.Lock()
		defer mu.Unlock()
		traceTimings["dns_ms"] = time.Since(dnsStart).Milliseconds()
		if info.Err == nil {
			phase = "connect"
		}
	}, TLSHandshakeStart: func() { mu.Lock(); defer mu.Unlock(); tlsStart = time.Now(); phase = "tls" }, TLSHandshakeDone: func(_ tls.ConnectionState, handshakeErr error) {
		mu.Lock()
		defer mu.Unlock()
		traceTimings["tls_ms"] = time.Since(tlsStart).Milliseconds()
		if handshakeErr == nil {
			phase = "http"
		}
	}, GotConn: func(i httptrace.GotConnInfo) {
		mu.Lock()
		defer mu.Unlock()
		traceFacts["client_connection_reused"] = strconv.FormatBool(i.Reused)
	}, GotFirstResponseByte: func() {
		mu.Lock()
		defer mu.Unlock()
		traceTimings["first_byte_ms"] = time.Since(requestStart).Milliseconds()
	}}
	req, e := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), x.HTTP.Method, target, nil)
	if e != nil {
		return local("setup", "invalid_request")
	}
	for k, v := range x.HTTP.RequestHeaders {
		req.Header.Set(k, v)
	}
	token, e := secure.SecretEnv(x.HTTP.BearerEnv)
	if e != nil {
		return local("setup", "http_secret_missing")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", "Topophany/"+model.Version)
	resp, e := client.Do(req)
	if e != nil {
		mu.Lock()
		p := phase
		mu.Unlock()
		out, st, code := classify(e)
		if st == "connect" {
			st = p
		}
		return &failure{stage: st, code: code, local: out == "error"}
	}
	defer resp.Body.Close()
	o.Connectivity = true
	o.Facts["http_status"] = strconv.Itoa(resp.StatusCode)
	o.Facts["response_proof"] = "http_response"
	if resp.TLS != nil {
		o.Facts["response_proof"] = "verified_https"
	}
	limit := x.HTTP.MaxBodyBytes
	if limit <= 0 {
		limit = 65536
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return fail("http_body", e)
	}
	if int64(len(body)) > limit {
		return local("http_body", "body_budget_exceeded")
	}
	accepted := resp.StatusCode >= 200 && resp.StatusCode < 300
	if len(x.HTTP.Status) > 0 {
		accepted = false
		for _, v := range x.HTTP.Status {
			if resp.StatusCode == v {
				accepted = true
			}
		}
	}
	if !accepted {
		return &failure{stage: "http_assert", code: "unexpected_status"}
	}
	if x.HTTP.Contains != "" && !strings.Contains(string(body), x.HTTP.Contains) {
		return &failure{stage: "http_assert", code: "body_mismatch"}
	}
	if x.HTTP.BodyRegex != "" {
		re, e := regexp.Compile(x.HTTP.BodyRegex)
		if e != nil {
			return local("setup", "invalid_regex")
		}
		if !re.Match(body) {
			return &failure{stage: "http_assert", code: "regex_mismatch"}
		}
	}
	for k, v := range x.HTTP.Headers {
		if resp.Header.Get(k) != v {
			return &failure{stage: "http_assert", code: "header_mismatch"}
		}
	}
	if len(x.HTTP.ExpectedExit) > 0 {
		ip, e := exitIP(body, x.HTTP)
		if e != nil {
			return &failure{stage: "exit_assert", code: "exit_not_observed"}
		}
		o.Facts["exit_ip"] = ip.String()
		ok := false
		for _, v := range x.HTTP.ExpectedExit {
			if addr, e := netip.ParseAddr(v); e == nil {
				ok = ok || addr.Unmap() == ip.Unmap()
			} else if prefix, e := netip.ParsePrefix(v); e == nil {
				ok = ok || prefix.Contains(ip) || prefix.Contains(ip.Unmap())
			}
		}
		if !ok {
			o.Outcome = "policy"
			o.Stage = "exit_assert"
			o.Code = "exit_mismatch"
			o.Detail = "request succeeded but observed exit violates the declared policy"
			return nil
		}
	}
	o.Outcome = "ok"
	o.Stage = "http_assert"
	o.Detail = "HTTP assertions passed; result applies only to this request"
	return nil
}
func exitIP(b []byte, h model.HTTPOptions) (netip.Addr, error) {
	s := strings.TrimSpace(string(b))
	switch h.ExitFormat {
	case "trace":
		s = ""
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "ip=") {
				s = strings.TrimSpace(strings.TrimPrefix(line, "ip="))
				break
			}
		}
	case "json":
		var v map[string]json.RawMessage
		if e := json.Unmarshal(b, &v); e != nil {
			return netip.Addr{}, e
		}
		key := h.ExitJSONKey
		if key == "" {
			key = "ip"
		}
		if e := json.Unmarshal(v[key], &s); e != nil {
			return netip.Addr{}, e
		}
	}
	return netip.ParseAddr(s)
}
