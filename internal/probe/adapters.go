package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

// Only these three Mihomo API reads are permitted. In particular GET delay
// endpoints are excluded: some of them clear a policy group's fixed selection.
var mihomoReads = map[string]bool{"/configs": true, "/proxies": true, "/providers/proxies": true}

type coreProxy struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Now         string   `json:"now"`
	All         []string `json:"all"`
	DialerProxy string   `json:"dialer-proxy"`
	TestURL     string   `json:"testUrl"`
	Alive       *bool    `json:"alive"`
	History     []struct {
		Time  time.Time `json:"time"`
		Delay int       `json:"delay"`
	} `json:"history"`
}

func readCore(ctx context.Context, c *http.Client, base, path, token string, v any) error {
	if !mihomoReads[path] {
		return local("adapter", "api_path_not_allowed")
	}
	u, e := url.Parse(base)
	if e != nil {
		return local("setup", "invalid_adapter_url")
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = ""
	u.Fragment = ""
	req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return local("setup", "invalid_adapter_url")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := c.Do(req)
	if e != nil {
		return fail("adapter_api", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return local("adapter_api", "authentication_failed")
	}
	if resp.StatusCode != 200 {
		return &failure{stage: "adapter_api", code: fmt.Sprintf("http_%d", resp.StatusCode)}
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if e != nil {
		return fail("adapter_api", e)
	}
	if len(b) > 2<<20 {
		return local("adapter_api", "response_limit_exceeded")
	}
	// Bound proxy/provider maps, member/history arrays and aggregate containers
	// before allocating typed response objects, including ignored fields.
	if e = secure.JSONResponseBudget(b, 128, 256, 4096); e != nil {
		return local("adapter_api", "response_complexity_exceeded")
	}
	if e = json.Unmarshal(b, v); e != nil {
		return local("adapter_api", "unsupported_response_shape")
	}
	return nil
}
func (r Runner) mihomo(ctx context.Context, d Dialer, x model.Check, target string, o *model.Observation) error {
	token, e := secure.SecretEnv(x.Adapter.TokenEnv)
	if e != nil {
		return local("setup", "adapter_secret_missing")
	}
	u, e := url.Parse(target)
	if e != nil {
		return local("setup", "invalid_adapter_url")
	}
	tlsName := u.Hostname()
	if x.ServerName != "" {
		tlsName = x.ServerName
	}
	tc, e := TLSConfig(tlsName, x.CAFile)
	if e != nil {
		return e
	}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: tc, MaxResponseHeaderBytes: 32768, DialContext: func(ctx context.Context, n, a string) (net.Conn, error) {
		return d.DialContext(ctx, n, pinned(a, x.ResolveIP))
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var cfg struct {
		Mode string `json:"mode"`
		TUN  struct {
			Enable bool `json:"enable"`
		} `json:"tun"`
	}
	if e = readCore(ctx, client, target, "/configs", token, &cfg); e != nil {
		return e
	}
	if cfg.Mode == "" {
		return local("adapter_api", "missing_mode")
	}
	var proxies struct {
		Proxies map[string]coreProxy `json:"proxies"`
	}
	if e = readCore(ctx, client, target, "/proxies", token, &proxies); e != nil {
		return e
	}
	if proxies.Proxies == nil {
		return local("adapter_api", "missing_proxies")
	}
	o.PathEvidence = "runtime_reported_not_request_verified"
	o.Facts["sampling_atomicity"] = "separate_API_reads_not_atomic"
	o.Facts["mode"] = safeFact(cfg.Mode)
	o.Facts["tun_enabled"] = strconv.FormatBool(cfg.TUN.Enable)
	o.Facts["provider_parameters"] = "not_verifiable_by_names_alone"
	o.Outcome = "ok"
	o.Stage = "adapter"
	o.Detail = "read-only runtime telemetry collected; not an end-to-end service check"
	if x.Adapter.ExpectedMode != "" && cfg.Mode != x.Adapter.ExpectedMode {
		o.Outcome = "policy"
		o.Code = "mode_mismatch"
		o.Detail = "runtime mode differs from declared policy"
	}
	if x.Adapter.RootProxy != "" {
		chain, e := expandProxy(proxies.Proxies, x.Adapter.RootProxy)
		if e != nil {
			o.Facts["chain_resolution"] = "unresolved"
			if o.Outcome != "policy" {
				o.Outcome = "unknown"
				o.Code = "runtime_chain_unresolved"
				o.Detail = "runtime chain unresolved (missing selection, unsupported shape or cycle)"
			}
		} else {
			o.Facts["reported_chain"] = safeFact(strings.Join(chain, " -> "))
		}
		if p, ok := proxies.Proxies[x.Adapter.RootProxy]; ok {
			o.Facts["reported_selection"] = safeFact(p.Now)
			o.Facts["test_url"] = publicURL(p.TestURL)
			if len(p.History) > 0 {
				last := p.History[len(p.History)-1]
				o.Facts["core_last_checked"] = last.Time.UTC().Format(time.RFC3339)
				o.Facts["core_delay_ms"] = strconv.Itoa(last.Delay)
			}
		}
	}
	if len(x.Adapter.ExpectedProviders) > 0 {
		// Reserve room for all fixed observation fields and bound map keys to
		// the collector contract before reading/retaining provider telemetry.
		if len(x.Adapter.ExpectedProviders) > 16 {
			return local("setup", "provider_evidence_budget_exceeded")
		}
		for k := range x.Adapter.ExpectedProviders {
			if len(k) > 119 || secure.Text(k) != k {
				return local("setup", "invalid_provider_name")
			}
		}
		var out struct {
			Providers map[string]struct {
				Proxies   []coreProxy `json:"proxies"`
				UpdatedAt time.Time   `json:"updatedAt"`
			} `json:"providers"`
		}
		if e = readCore(ctx, client, target, "/providers/proxies", token, &out); e != nil {
			return e
		}
		keys := make([]string, 0, len(x.Adapter.ExpectedProviders))
		for k := range x.Adapter.ExpectedProviders {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p, ok := out.Providers[k]
			actual := []string{}
			for _, v := range p.Proxies {
				actual = append(actual, v.Name)
			}
			want := x.Adapter.ExpectedProviders[k]
			same := ok && len(actual) == len(want)
			if same {
				for i := range want {
					if actual[i] != want[i] {
						same = false
						break
					}
				}
			}
			o.Facts["provider."+safeFact(k)] = safeFact(strings.Join(actual, ","))
			if !same {
				o.Outcome = "policy"
				o.Code = "provider_membership_mismatch"
				o.Detail = "desired provider membership/order differs from the loaded runtime"
			}
		}
	}
	return nil
}
func expandProxy(all map[string]coreProxy, root string) ([]string, error) {
	state := map[string]bool{}
	var visit func(string, int) ([]string, error)
	visit = func(name string, depth int) ([]string, error) {
		if depth > 64 || state[name] {
			return nil, errors.New("proxy reference cycle/depth")
		}
		if name == "DIRECT" {
			return []string{"DIRECT"}, nil
		}
		p, ok := all[name]
		if !ok {
			return nil, errors.New("missing proxy")
		}
		state[name] = true
		defer delete(state, name)
		switch strings.ToLower(p.Type) {
		case "loadbalance", "load-balance":
			return nil, errors.New("load-balanced request path is unknown")
		case "reject", "rejectdrop":
			return nil, errors.New("reject policy")
		case "selector", "select", "fallback", "urltest", "url-test":
			if p.Now == "" {
				return nil, errors.New("no selected member")
			}
			return visit(p.Now, depth+1)
		}
		if p.Now != "" {
			return visit(p.Now, depth+1)
		}
		if len(p.All) > 0 {
			return nil, errors.New("unknown group shape")
		}
		chain := []string{}
		if p.DialerProxy != "" {
			prefix, e := visit(p.DialerProxy, depth+1)
			if e != nil {
				return nil, e
			}
			chain = append(chain, prefix...)
		}
		return append(chain, name), nil
	}
	return visit(root, 0)
}
func safeFact(s string) string {
	s = secure.Text(s)
	if len(s) > 2048 {
		n := 2045
		for !utf8.RuneStart(s[n]) {
			n--
		}
		s = s[:n] + "…"
	}
	return s
}
func publicURL(s string) string {
	u, e := url.Parse(s)
	if e != nil {
		return ""
	}
	u.User = nil
	u.Fragment = ""
	if u.RawQuery != "" {
		u.RawQuery = "redacted"
	}
	return safeFact(u.String())
}

type cappedOutput struct {
	b     bytes.Buffer
	limit int
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	if w.b.Len()+len(p) > w.limit {
		return 0, errors.New("command output budget exceeded")
	}
	return w.b.Write(p)
}
func defaultCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name != "tailscale" && name != "wg" {
		return nil, errors.New("command not allowed")
	}
	p, e := exec.LookPath(name)
	if e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, p, args...)
	cmd.WaitDelay = time.Second
	w := &cappedOutput{limit: 2 << 20}
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil {
		return nil, e
	}
	return w.b.Bytes(), nil
}
func (r Runner) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.Command != nil {
		return r.Command(ctx, name, args...)
	}
	return defaultCommand(ctx, name, args...)
}
func (r Runner) tailscale(ctx context.Context, x model.Check, o *model.Observation) error {
	b, e := r.command(ctx, "tailscale", "status", "--json")
	if e != nil {
		return local("adapter", "tailscale_unavailable_or_permission_denied")
	}
	if len(b) > 2<<20 {
		return local("adapter", "response_limit_exceeded")
	}
	var status struct {
		BackendState string `json:"BackendState"`
		Peer         map[string]struct {
			ID           string    `json:"ID"`
			HostName     string    `json:"HostName"`
			DNSName      string    `json:"DNSName"`
			TailscaleIPs []string  `json:"TailscaleIPs"`
			Online       bool      `json:"Online"`
			Active       bool      `json:"Active"`
			CurAddr      string    `json:"CurAddr"`
			Relay        string    `json:"Relay"`
			LastSeen     time.Time `json:"LastSeen"`
		} `json:"Peer"`
	}
	if e = json.Unmarshal(b, &status); e != nil || status.BackendState == "" {
		return local("adapter", "unsupported_tailscale_response")
	}
	o.Facts["backend_state"] = safeFact(status.BackendState)
	for key, p := range status.Peer {
		match := key == x.Adapter.Peer || p.ID == x.Adapter.Peer || p.HostName == x.Adapter.Peer || strings.TrimSuffix(p.DNSName, ".") == strings.TrimSuffix(x.Adapter.Peer, ".")
		for _, ip := range p.TailscaleIPs {
			match = match || ip == x.Adapter.Peer
		}
		if !match {
			continue
		}
		o.Outcome = "ok"
		o.Stage = "adapter"
		o.PathEvidence = "peer_local_runtime_reported"
		o.Facts["online_reported"] = strconv.FormatBool(p.Online)
		o.Facts["active_reported"] = strconv.FormatBool(p.Active)
		o.Facts["last_seen"] = p.LastSeen.UTC().Format(time.RFC3339)
		o.Detail = "peer telemetry collected; idle is not offline; probe tailnet service separately"
		if p.CurAddr != "" {
			o.Facts["transport_hint"] = "direct_endpoint_reported"
		} else if p.Relay != "" {
			o.Facts["transport_hint"] = "relay_preference_reported"
			o.Facts["relay"] = safeFact(p.Relay)
		} else {
			o.Facts["transport_hint"] = "unknown"
		}
		if !p.Online || status.BackendState != "Running" {
			o.Outcome = "unknown"
			o.Code = "peer_or_backend_not_online"
			o.Detail = "peer/backend not reported online; host power state and service reachability unknown"
		}
		return nil
	}
	o.Outcome = "unknown"
	o.Stage = "adapter"
	o.Code = "peer_not_found"
	o.Detail = "peer absent from this observer's status"
	return nil
}
func (r Runner) wireguard(ctx context.Context, x model.Check, o *model.Observation) error {
	b, e := r.command(ctx, "wg", "show", x.Adapter.Interface, "dump")
	if e != nil {
		return local("adapter", "wireguard_unavailable_or_permission_denied")
	}
	if len(b) > 2<<20 {
		return local("adapter", "response_limit_exceeded")
	}
	// The first line includes the interface PRIVATE KEY. It is intentionally
	// discarded; peer preshared keys (column 2) are never retained either.
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) < 1 || len(strings.Fields(lines[0])) < 4 {
		return local("adapter", "unsupported_wireguard_response")
	}
	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if len(f) != 8 {
			continue
		}
		if f[0] != x.Adapter.Peer {
			continue
		}
		hs, e := strconv.ParseInt(f[4], 10, 64)
		if e != nil {
			return local("adapter", "invalid_handshake_timestamp")
		}
		rx, e := strconv.ParseUint(f[5], 10, 64)
		if e != nil {
			return local("adapter", "invalid_transfer_counter")
		}
		tx, e := strconv.ParseUint(f[6], 10, 64)
		if e != nil {
			return local("adapter", "invalid_transfer_counter")
		}
		o.Outcome = "ok"
		o.Stage = "adapter"
		o.PathEvidence = "interface_local_runtime_reported"
		o.Facts["rx_bytes"] = strconv.FormatUint(rx, 10)
		o.Facts["tx_bytes"] = strconv.FormatUint(tx, 10)
		if hs > 0 {
			o.Facts["last_handshake"] = time.Unix(hs, 0).UTC().Format(time.RFC3339)
		}
		o.Detail = "peer counters collected; an old handshake alone is not a tunnel failure; probe an inner endpoint"
		return nil
	}
	o.Outcome = "unknown"
	o.Stage = "adapter"
	o.Code = "peer_not_found"
	o.Detail = "peer not present on the configured local interface"
	return nil
}
